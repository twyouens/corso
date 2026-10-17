package seed

import (
	"context"
	"fmt"
	"time"

	"github.com/alcionai/clues"
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

		items = append(items, ManifestItem{
			Workload:   WorkloadExchangeCalendar,
			Kind:       "event",
			ID:         ptr.Val(ev.GetId()),
			Name:       want.subject,
			FolderPath: calendarName,
			CreatedAt:  ev.GetCreatedDateTime(),
			ModifiedAt: ev.GetLastModifiedDateTime(),
			ResourceID: userID,
		})
	}

	return items, summary, nil
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
