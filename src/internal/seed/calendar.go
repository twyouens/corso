package seed

import (
	"context"
	"fmt"
	"time"

	"github.com/alcionai/clues"
	"github.com/microsoft/kiota-abstractions-go/serialization"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/microsoftgraph/msgraph-sdk-go/users"

	"github.com/twyouens/corso/src/internal/common/ptr"
	"github.com/twyouens/corso/src/pkg/dttm"
	"github.com/twyouens/corso/src/pkg/services/m365/api"
	"github.com/twyouens/corso/src/pkg/services/m365/api/graph"
)

// calendarName is the single dedicated calendar the seeder owns in the
// seeded user's mailbox.
const calendarName = Prefix + "-calendar"

// calendarEvents is the fixed, known content the seeder writes. Start times
// are anchored to a fixed future offset from each seeder run's start rather
// than a literal timestamp, so a stale seeded event doesn't drift into the
// past across repeated runs over weeks; since the event is matched (and
// reused, never recreated) by subject alone, its recorded start time is
// whatever it was the run that created it, and that's what the manifest
// records.
var calendarEvents = []struct {
	subject   string
	startFrom time.Time
	duration  time.Duration
}{
	{subject: Prefix + "-event-001", startFrom: time.Now().Add(24 * time.Hour), duration: 30 * time.Minute},
}

// seedCalendar idempotently ensures the dedicated calendar and its known
// events exist in userID's mailbox, and returns a manifest item for each,
// plus how many of each were found versus freshly created.
func seedCalendar(ctx context.Context, c api.Client, userID string) ([]ManifestItem, WorkloadSummary, error) {
	summary := WorkloadSummary{Workload: WorkloadExchangeCalendar}
	events := c.Events()

	calendar, calCreated, err := FindOrCreate(
		func() (graph.Container, bool, error) {
			found, err := events.GetContainerByName(ctx, userID, "", calendarName)
			if err == nil {
				return found, true, nil
			}

			if ContainerNotFound(err) {
				return nil, false, nil
			}

			return nil, false, clues.Wrap(err, "looking up seed calendar")
		},
		func() (graph.Container, error) {
			created, err := events.CreateContainer(ctx, userID, "", calendarName)
			return created, clues.Wrap(err, "creating seed calendar").OrNil()
		})
	if err != nil {
		return nil, summary, err
	}

	summary.record(calCreated)

	calendarID := ptr.Val(calendar.GetId())

	// Calendar, like MailFolder and ContactFolder, has no
	// createdDateTime/lastModifiedDateTime in Graph at all (its model
	// embeds Entity, not OutlookItem), so the calendar's manifest item
	// below is correctly dateless; there is nothing to read.
	existing, err := listEventsBySubject(ctx, events, userID, calendarID)
	if err != nil {
		return nil, summary, clues.Wrap(err, "listing seed calendar contents")
	}

	items := []ManifestItem{
		{
			Workload:   WorkloadExchangeCalendar,
			Kind:       "calendar",
			ID:         calendarID,
			Name:       calendarName,
			ResourceID: userID,
		},
	}

	for _, want := range calendarEvents {
		ev, evCreated, err := FindOrCreate(
			func() (models.Eventable, bool, error) {
				e, found := existing[want.subject]
				return e, found, nil
			},
			func() (models.Eventable, error) {
				start := want.startFrom
				end := start.Add(want.duration)

				startTZ := models.NewDateTimeTimeZone()
				startTZ.SetDateTime(ptr.To(dttm.Format(start)))
				startTZ.SetTimeZone(ptr.To("UTC"))

				endTZ := models.NewDateTimeTimeZone()
				endTZ.SetDateTime(ptr.To(dttm.Format(end)))
				endTZ.SetTimeZone(ptr.To("UTC"))

				newEv := models.NewEvent()
				newEv.SetSubject(ptr.To(want.subject))
				newEv.SetStart(startTZ)
				newEv.SetEnd(endTZ)

				posted, err := events.PostItem(ctx, userID, calendarID, newEv)
				if err != nil {
					return nil, clues.Wrap(err, fmt.Sprintf("creating seed event %q", want.subject))
				}

				return posted, nil
			})
		if err != nil {
			return nil, summary, err
		}

		summary.record(evCreated)

		items = append(items, datedManifestItem(
			ev,
			WorkloadExchangeCalendar,
			"event",
			want.subject,
			calendarName,
			0,
			userID))
	}

	// One recurring series, with one cancelled occurrence and one
	// exception occurrence, to exercise the Graph SDK v1.104.0 upgrade's
	// promotion of cancelledOccurrences/exceptionOccurrences from
	// AdditionalData to typed fields (chunk 0.5) against real Graph data.
	// Every event above is a single, non-recurring occurrence; nothing
	// else in this harness ever populates either field.
	recurringItems, recurringCreated, err := seedRecurringCalendarEvent(ctx, events, userID, calendarID, existing)
	if err != nil {
		return nil, summary, clues.Wrap(err, "seeding recurring calendar event")
	}

	summary.record(recurringCreated)
	items = append(items, recurringItems...)

	return items, summary, nil
}

// recurringEventSubject is the one recurring event this harness seeds.
const recurringEventSubject = Prefix + "-event-recurring-001"

// recurringEventExceptionSuffix marks the one occurrence of
// recurringEventSubject's series that this package deliberately modifies
// (not cancels) to produce a Graph exception occurrence.
const recurringEventExceptionSuffix = " (exception)"

// recurringEventOccurrenceCount is deliberately small: enough to have a
// cancelled occurrence, an exception occurrence and at least one
// untouched occurrence, without creating hundreds of occurrences in a
// real tenant for a test harness.
const recurringEventOccurrenceCount = 5

// recurringEventCancelIndex and recurringEventExceptionIndex are 0-based
// positions into the chronologically-ordered instance list
// api.Events.GetItemInstances returns (Graph's documented default order
// for this endpoint). Both must be less than recurringEventOccurrenceCount
// and distinct from each other.
const (
	recurringEventCancelIndex    = 1
	recurringEventExceptionIndex = 3
)

// eventWithExceptionsBetaURLTemplate mirrors
// pkg/services/m365/api/events.go's own GetItem/fixupExceptionOccurrences
// pattern: Graph only returns exceptionOccurrences on a single-event GET
// through this beta endpoint with $expand=exceptionOccurrences - not on
// the stable calendar-listing GET listEventsBySubject uses, and not
// without the $expand regardless of endpoint.
//
//nolint:lll
const eventWithExceptionsBetaURLTemplate = "https://graph.microsoft.com/beta/users/%s/events/%s?$expand=exceptionOccurrences"

// seedRecurringCalendarEvent idempotently ensures recurringEventSubject's
// series exists in calendarID, with one occurrence cancelled and one
// turned into an exception, and returns a manifest item for the series
// (carrying both as a RecurrenceDetail) and a second manifest item for
// the one exception occurrence, so a later chunk can assert against it
// directly like any other item.
//
// The cancel-and-modify steps run only inside the create() branch FindOrCreate
// drives below, so they execute exactly once, the first time this series
// is ever seeded; a later run finds the series already existing and does
// nothing further to it - the same idempotent-upsert rule every other
// workload in this package follows. existing is the same subject->event
// map seedCalendar already built for the simple events above; this
// series participates in the same lookup.
func seedRecurringCalendarEvent(
	ctx context.Context,
	events api.Events,
	userID, calendarID string,
	existing map[string]models.Eventable,
) ([]ManifestItem, bool, error) {
	series, seriesCreated, err := FindOrCreate(
		func() (models.Eventable, bool, error) {
			e, found := existing[recurringEventSubject]
			return e, found, nil
		},
		func() (models.Eventable, error) {
			return createRecurringSeriesWithCancelledAndException(ctx, events, userID, calendarID)
		})
	if err != nil {
		return nil, false, err
	}

	// Whichever branch ran, re-fetch the series master through the one
	// call that actually returns cancelledOccurrences/exceptionOccurrences:
	// a single-event GET through the beta endpoint with
	// $expand=exceptionOccurrences. Neither the plain POST response above
	// nor listEventsBySubject's calendar-listing GET populates either
	// field - this mirrors the fix from the previous review round
	// (OneDrive/SharePoint file ModifiedAt): don't trust an object
	// obtained for a different purpose to carry a field that needs its
	// own authoritative read.
	enriched, err := getEventWithExceptions(ctx, events, userID, ptr.Val(series.GetId()))
	if err != nil {
		return nil, false, clues.Wrap(err, "fetching seed recurring event with its exceptions")
	}

	cancelledIDs := enriched.GetCancelledOccurrences()
	if len(cancelledIDs) == 0 {
		return nil, false, clues.New(
			"seed recurring event has no cancelled occurrences; the cancellation did not take effect")
	}

	exceptions := enriched.GetExceptionOccurrences()
	if len(exceptions) == 0 {
		return nil, false, clues.New(
			"seed recurring event has no exception occurrences; the modification did not take effect")
	}

	seriesItem := datedManifestItem(
		enriched,
		WorkloadExchangeCalendar,
		"event_recurring",
		recurringEventSubject,
		calendarName,
		0,
		userID)

	seriesItem.Recurrence = &RecurrenceDetail{
		CancelledOccurrenceIDs: cancelledIDs,
		ExceptionOccurrenceIDs: []string{ptr.Val(exceptions[0].GetId())},
	}

	exceptionItem := datedManifestItem(
		exceptions[0],
		WorkloadExchangeCalendar,
		"event_exception",
		ptr.Val(exceptions[0].GetSubject()),
		calendarName,
		0,
		userID)

	return []ManifestItem{seriesItem, exceptionItem}, seriesCreated, nil
}

// createRecurringSeriesWithCancelledAndException creates
// recurringEventSubject's series, then cancels one occurrence and
// modifies a different one into an exception. Both actions operate only
// on instance IDs of the series just created above - never on anything
// this package did not create, and the cancellation is a DELETE on that
// one occurrence's own instance ID, which is Microsoft's documented
// mechanism for cancelling a single occurrence of a recurring event
// (https://learn.microsoft.com/en-us/graph/api/event-delete): a
// legitimate calendar operation on an event this package owns, not a
// workaround. No other delete exists anywhere in this package.
func createRecurringSeriesWithCancelledAndException(
	ctx context.Context,
	events api.Events,
	userID, calendarID string,
) (models.Eventable, error) {
	start := time.Now().Add(48 * time.Hour)
	end := start.Add(30 * time.Minute)

	startTZ := models.NewDateTimeTimeZone()
	startTZ.SetDateTime(ptr.To(dttm.Format(start)))
	startTZ.SetTimeZone(ptr.To("UTC"))

	endTZ := models.NewDateTimeTimeZone()
	endTZ.SetDateTime(ptr.To(dttm.Format(end)))
	endTZ.SetTimeZone(ptr.To("UTC"))

	pattern := models.NewRecurrencePattern()
	pattern.SetTypeEscaped(ptr.To(models.DAILY_RECURRENCEPATTERNTYPE))
	pattern.SetInterval(ptr.To(int32(1)))

	rrange := models.NewRecurrenceRange()
	rrange.SetTypeEscaped(ptr.To(models.NUMBERED_RECURRENCERANGETYPE))
	rrange.SetStartDate(serialization.NewDateOnly(start))
	rrange.SetNumberOfOccurrences(ptr.To(int32(recurringEventOccurrenceCount)))

	recurrence := models.NewPatternedRecurrence()
	recurrence.SetPattern(pattern)
	recurrence.SetRangeEscaped(rrange)

	newEv := models.NewEvent()
	newEv.SetSubject(ptr.To(recurringEventSubject))
	newEv.SetStart(startTZ)
	newEv.SetEnd(endTZ)
	newEv.SetRecurrence(recurrence)

	series, err := events.PostItem(ctx, userID, calendarID, newEv)
	if err != nil {
		return nil, clues.Wrap(err, "creating seed recurring event series")
	}

	seriesID := ptr.Val(series.GetId())

	// A window comfortably wider than the series itself, so every
	// occurrence the pattern produces is in range regardless of exactly
	// how Graph aligns the first occurrence to the requested start time.
	instancesStart := start.Add(-24 * time.Hour)
	instancesEnd := start.Add(time.Duration(recurringEventOccurrenceCount+2) * 24 * time.Hour)

	instances, err := events.GetItemInstances(
		ctx,
		userID,
		seriesID,
		dttm.Format(instancesStart),
		dttm.Format(instancesEnd))
	if err != nil {
		return nil, clues.Wrap(err, "listing seed recurring event's occurrences")
	}

	if len(instances) <= recurringEventExceptionIndex {
		return nil, clues.New(fmt.Sprintf(
			"seed recurring event series produced %d occurrence(s), need more than %d",
			len(instances), recurringEventExceptionIndex))
	}

	cancelID := ptr.Val(instances[recurringEventCancelIndex].GetId())

	if err := events.DeleteItem(ctx, userID, cancelID); err != nil {
		return nil, clues.Wrap(err, "cancelling one occurrence of the seed recurring event")
	}

	exceptionID := ptr.Val(instances[recurringEventExceptionIndex].GetId())

	exceptionBody := models.NewEvent()
	exceptionBody.SetSubject(ptr.To(recurringEventSubject + recurringEventExceptionSuffix))

	if _, err := events.PatchItem(ctx, userID, exceptionID, exceptionBody); err != nil {
		return nil, clues.Wrap(err, "modifying one occurrence of the seed recurring event into an exception")
	}

	return series, nil
}

// getEventWithExceptions fetches eventID through the one endpoint shape
// that actually populates exceptionOccurrences - see
// eventWithExceptionsBetaURLTemplate's comment.
func getEventWithExceptions(
	ctx context.Context,
	events api.Events,
	userID, eventID string,
) (models.Eventable, error) {
	rawURL := fmt.Sprintf(eventWithExceptionsBetaURLTemplate, userID, eventID)

	event, err := users.
		NewItemEventsEventItemRequestBuilder(rawURL, events.Stable.Adapter()).
		Get(ctx, nil)
	if err != nil {
		return nil, clues.Wrap(err, "getting event")
	}

	return event, nil
}

// eventsBySubject builds a subject->event map from an already-fetched
// slice of events. Split out from listEventsBySubject so the map-building
// logic - the actual idempotency decision input - is unit-testable without
// a Graph call.
func eventsBySubject(evs []models.Eventable) map[string]models.Eventable {
	out := map[string]models.Eventable{}

	for _, ev := range evs {
		out[ptr.Val(ev.GetSubject())] = ev
	}

	return out
}

// listEventsBySubject returns a subject->event map of the events already in
// calendarID, so seedCalendar can decide per-event whether to create it,
// and - whether found or freshly created - read the same real Graph dates
// either way. api.Events has no "list events" wrapper (only GetItem-by-ID
// and PostItem), so this uses the same raw-client escape hatch as
// listMessagesBySubject.
func listEventsBySubject(
	ctx context.Context,
	events api.Events,
	userID, calendarID string,
) (map[string]models.Eventable, error) {
	resp, err := events.Stable.
		Client().
		Users().
		ByUserId(userID).
		Calendars().
		ByCalendarId(calendarID).
		Events().
		Get(ctx, &users.ItemCalendarsItemEventsRequestBuilderGetRequestConfiguration{
			QueryParameters: &users.ItemCalendarsItemEventsRequestBuilderGetQueryParameters{
				Select: []string{"id", "subject", "createdDateTime", "lastModifiedDateTime"},
			},
		})
	if err != nil {
		return nil, clues.Wrap(err, "listing events")
	}

	return eventsBySubject(resp.GetValue()), nil
}
