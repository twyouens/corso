package operations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/twyouens/corso/src/internal/model"
	"github.com/twyouens/corso/src/internal/tester"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/fault"
)

// TestClassifyBackupType is chunk 0.6's review-round-3 fix: the two tests
// nearest this logic before this one (pkg/engine's
// TestListBackupPoints_IncludesAssistBackups and this package's own
// TestMergeAdditionalTags) both replicate the model.AssistBackup tag by
// hand - constructing a *backup.Backup or a tags map that already carries
// it - rather than exercising the decision that actually produces it.
// Nobody had proven that a live partial failure really gets classified
// assist rather than some other value, or that classification does not
// silently stop happening a third way. This calls classifyBackupType -
// createBackupModels' own classification logic, extracted verbatim so it
// is testable without a real streamstore and model store - directly, for
// every branch: a clean run (merge), one recoverable error under
// FailAfterRecovery (assist - the live scenario fix 1 is about), one
// recoverable error under BestEffort (merge: isMergeBackup short-circuits
// true for BestEffort regardless of recovered count, and is checked
// before isAssistBackup in both this function and the original inline
// switch it replaces), a hard failure (none of the three, an error), and
// preview mode both succeeding and failing its own merge-equivalent
// check.
func TestClassifyBackupType(t *testing.T) {
	ctx, flush := tester.NewContext(t)
	defer flush()

	table := []struct {
		name           string
		previewEnabled bool
		snapID, ssid   string
		failurePolicy  control.FailurePolicy
		hasNewDeets    bool
		errs           func() *fault.Bus
		expectType     string
		expectErr      bool
	}{
		{
			name:          "clean run with no errors is a merge backup",
			snapID:        "snap-1",
			ssid:          "ssid-1",
			failurePolicy: control.FailAfterRecovery,
			errs:          func() *fault.Bus { return fault.New(false) },
			expectType:    model.MergeBackup,
		},
		{
			name:          "one recoverable error under FailAfterRecovery is an assist backup",
			snapID:        "snap-1",
			ssid:          "ssid-1",
			failurePolicy: control.FailAfterRecovery,
			hasNewDeets:   true,
			errs: func() *fault.Bus {
				b := fault.New(false)
				b.AddRecoverable(ctx, assert.AnError)
				return b
			},
			expectType: model.AssistBackup,
		},
		{
			name:          "one recoverable error under FailAfterRecovery with no new details fits no branch",
			snapID:        "snap-1",
			ssid:          "ssid-1",
			failurePolicy: control.FailAfterRecovery,
			hasNewDeets:   false,
			errs: func() *fault.Bus {
				b := fault.New(false)
				b.AddRecoverable(ctx, assert.AnError)
				return b
			},
			expectErr: true,
		},
		{
			name:          "one recoverable error under BestEffort is still a merge backup",
			snapID:        "snap-1",
			ssid:          "ssid-1",
			failurePolicy: control.BestEffort,
			errs: func() *fault.Bus {
				b := fault.New(false)
				b.AddRecoverable(ctx, assert.AnError)
				return b
			},
			expectType: model.MergeBackup,
		},
		{
			name:          "a hard failure fits no branch",
			snapID:        "snap-1",
			ssid:          "ssid-1",
			failurePolicy: control.FailAfterRecovery,
			errs: func() *fault.Bus {
				return fault.New(false).Fail(assert.AnError)
			},
			expectErr: true,
		},
		{
			name:          "missing snapshot ID fits no branch",
			snapID:        "",
			ssid:          "ssid-1",
			failurePolicy: control.FailAfterRecovery,
			errs:          func() *fault.Bus { return fault.New(false) },
			expectErr:     true,
		},
		{
			name:           "preview mode with a clean run is a preview backup",
			previewEnabled: true,
			snapID:         "snap-1",
			ssid:           "ssid-1",
			failurePolicy:  control.FailAfterRecovery,
			errs:           func() *fault.Bus { return fault.New(false) },
			expectType:     model.PreviewBackup,
		},
		{
			name:           "preview mode with a hard failure is rejected, not silently merged/assisted",
			previewEnabled: true,
			snapID:         "snap-1",
			ssid:           "ssid-1",
			failurePolicy:  control.FailAfterRecovery,
			hasNewDeets:    true,
			errs: func() *fault.Bus {
				return fault.New(false).Fail(assert.AnError)
			},
			expectErr: true,
		},
	}

	for _, test := range table {
		t.Run(test.name, func(t *testing.T) {
			backupType, err := classifyBackupType(
				test.previewEnabled,
				test.snapID,
				test.ssid,
				test.failurePolicy,
				test.hasNewDeets,
				test.errs())

			if test.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.expectType, backupType)
		})
	}
}
