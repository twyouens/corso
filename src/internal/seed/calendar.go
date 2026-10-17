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
// events exist in userID's mailbox, and returns a manifest item for each.
func seedCalendar(ctx context.Context, c api.Client, userID string) ([]ManifestItem, error) {
	events := c.Events()

	calendar, _, err := FindOrCreate(
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
		return nil, err
	}

	calendarID := ptr.Val(calendar.GetId())

	existing, err := listEventIDsBySubject(ctx, events, userID, calendarID)
	if err != nil {
		return nil, clues.Wrap(err, "listing seed calendar contents")
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
		evID, created, err := FindOrCreate(
			func() (string, bool, error) {
				id, found := existing[want.subject]
				return id, found, nil
			},
			func() (string, error) {
				start := want.startFrom
				end := start.Add(want.duration)

				startTZ := models.NewDateTimeTimeZone()
				startTZ.SetDateTime(ptr.To(dttm.Format(start)))
				startTZ.SetTimeZone(ptr.To("UTC"))

				endTZ := models.NewDateTimeTimeZone()
				endTZ.SetDateTime(ptr.To(dttm.Format(end)))
				endTZ.SetTimeZone(ptr.To("UTC"))

				ev := models.NewEvent()
				ev.SetSubject(ptr.To(want.subject))
				ev.SetStart(startTZ)
				ev.SetEnd(endTZ)

				posted, err := events.PostItem(ctx, userID, calendarID, ev)
				if err != nil {
					return "", clues.Wrap(err, fmt.Sprintf("creating seed event %q", want.subject))
				}

				return ptr.Val(posted.GetId()), nil
			})
		if err != nil {
			return nil, err
		}

		item := ManifestItem{
			Workload:   WorkloadExchangeCalendar,
			Kind:       "event",
			ID:         evID,
			Name:       want.subject,
			FolderPath: calendarName,
			ResourceID: userID,
		}

		if created {
			item.CreatedAt = time.Now().UTC()
		}

		items = append(items, item)
	}

	return items, nil
}

// listEventIDsBySubject returns a subject->id map of the events already in
// calendarID, so SeedCalendar can decide per-event whether to create it.
// api.Events has no "list events" wrapper (only GetItem-by-ID and PostItem),
// so this uses the same raw-client escape hatch as listMessageIDsBySubject.
func listEventIDsBySubject(
	ctx context.Context,
	events api.Events,
	userID, calendarID string,
) (map[string]string, error) {
	resp, err := events.Stable.
		Client().
		Users().
		ByUserId(userID).
		Calendars().
		ByCalendarId(calendarID).
		Events().
		Get(ctx, &users.ItemCalendarsItemEventsRequestBuilderGetRequestConfiguration{
			QueryParameters: &users.ItemCalendarsItemEventsRequestBuilderGetQueryParameters{
				Select: []string{"id", "subject"},
			},
		})
	if err != nil {
		return nil, clues.Wrap(err, "listing events")
	}

	out := map[string]string{}

	for _, ev := range resp.GetValue() {
		out[ptr.Val(ev.GetSubject())] = ptr.Val(ev.GetId())
	}

	return out, nil
}
