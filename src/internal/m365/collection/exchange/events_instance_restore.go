package exchange

import (
	"context"
	"strings"
	"time"

	"github.com/alcionai/clues"
	"github.com/microsoftgraph/msgraph-sdk-go/models"

	"github.com/twyouens/corso/src/internal/common/ptr"
	"github.com/twyouens/corso/src/pkg/dttm"
	"github.com/twyouens/corso/src/pkg/fault"
)

type eventInstanceAndAttachmenter interface {
	attachmentGetDeletePoster
	DeleteItem(
		ctx context.Context,
		userID, itemID string,
	) error
	GetItemInstances(
		ctx context.Context,
		userID, itemID string,
		startDate, endDate string,
	) ([]models.Eventable, error)
	PatchItem(
		ctx context.Context,
		userID, eventID string,
		body models.Eventable,
	) (models.Eventable, error)
}

func updateRecurringEvents(
	ctx context.Context,
	eiaa eventInstanceAndAttachmenter,
	userID, containerID, itemID string,
	event models.Eventable,
	errs *fault.Bus,
) error {
	if event.GetRecurrence() == nil {
		return nil
	}

	// Cancellations and exceptions are typed fields on Eventable (promoted
	// from the beta API to stable by the Graph SDK that fetched this event).
	cancelledOccurrences := event.GetCancelledOccurrences()
	exceptionOccurrences := event.GetExceptionOccurrences()

	err := updateCancelledOccurrences(ctx, eiaa, userID, itemID, cancelledOccurrences)
	if err != nil {
		return clues.Wrap(err, "update cancelled occurrences")
	}

	err = updateExceptionOccurrences(ctx, eiaa, userID, containerID, itemID, exceptionOccurrences, errs)
	if err != nil {
		return clues.Wrap(err, "update exception occurrences")
	}

	return nil
}

// updateExceptionOccurrences take events that have exceptions, uses
// the originalStart date to find the instance and modify it to match
// the backup by updating the instance to match the backed up one
func updateExceptionOccurrences(
	ctx context.Context,
	eiaa eventInstanceAndAttachmenter,
	userID string,
	containerID string,
	itemID string,
	exceptionOccurrences []models.Eventable,
	errs *fault.Bus,
) error {
	for _, evt := range exceptionOccurrences {
		start := ptr.Val(evt.GetOriginalStart())
		startStr := dttm.FormatTo(start, dttm.DateOnly)
		endStr := dttm.FormatTo(start.Add(24*time.Hour), dttm.DateOnly)

		ictx := clues.Add(ctx, "event_instance_id", ptr.Val(evt.GetId()), "event_instance_date", start)

		// Get all instances on the day of the instance which should
		// just the one we need to modify
		instances, err := eiaa.GetItemInstances(ictx, userID, itemID, startStr, endStr)
		if err != nil {
			return clues.Wrap(err, "getting instances")
		}

		// Since the min recurrence interval is 1 day and we are
		// querying for only a single day worth of instances, we
		// should not have more than one instance here.
		if len(instances) != 1 {
			return clues.New("invalid number of instances for modified").
				With("instances_count", len(instances), "search_start", startStr, "search_end", endStr)
		}

		evt = toEventSimplified(evt)

		_, err = eiaa.PatchItem(ictx, userID, ptr.Val(instances[0].GetId()), evt)
		if err != nil {
			return clues.Wrap(err, "updating event instance")
		}

		// toEventSimplified mutates evt in place (and returns the same
		// value) but never touches its attachments, so evt still carries
		// whatever attachments fixupExceptionOccurrences attached to this
		// exception occurrence at backup time.
		err = updateAttachments(
			ictx,
			eiaa,
			userID,
			containerID,
			ptr.Val(instances[0].GetId()),
			evt,
			errs)
		if err != nil {
			return clues.Wrap(err, "updating event instance attachments")
		}
	}

	return nil
}

// updateCancelledOccurrences get the cancelled occurrences which is a
// list of strings of the format "<id>.<date>", parses the date out of
// that and uses the to get the event instance at that date to delete.
func updateCancelledOccurrences(
	ctx context.Context,
	eiaa eventInstanceAndAttachmenter,
	userID string,
	itemID string,
	cancelledOccurrences []string,
) error {
	// OPTIMIZATION: We can fetch a date range instead of fetching
	// instances if we have multiple cancelled events which are nearby
	// and reduce the number of API calls that we have to make
	for _, instance := range cancelledOccurrences {
		splits := strings.Split(instance, ".")

		startStr := splits[len(splits)-1]

		start, err := dttm.ParseTime(startStr)
		if err != nil {
			return clues.Wrap(err, "parsing cancelled event date")
		}

		endStr := dttm.FormatTo(start.Add(24*time.Hour), dttm.DateOnly)

		// Get all instances on the day of the instance which should
		// just the one we need to modify
		instances, err := eiaa.GetItemInstances(ctx, userID, itemID, startStr, endStr)
		if err != nil {
			return clues.Wrap(err, "getting instances")
		}

		// Since the min recurrence interval is 1 day and we are
		// querying for only a single day worth of instances, we
		// should not have more than one instance here.
		if len(instances) != 1 {
			return clues.New("invalid number of instances for cancelled").
				With("instances_count", len(instances), "search_start", startStr, "search_end", endStr)
		}

		err = eiaa.DeleteItem(ctx, userID, ptr.Val(instances[0].GetId()))
		if err != nil {
			return clues.Wrap(err, "deleting event instance")
		}
	}

	return nil
}
