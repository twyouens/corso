package operations

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/twyouens/corso/src/internal/model"
)

// TestMergeAdditionalTags proves chunk 0.6's rule that a caller-supplied
// backup tag (pkg/engine.BackupRequest.Tags, threaded through
// BackupOperation.AdditionalTags) can never collide with, and silently
// overwrite, a tag the fork's own bookkeeping already set
// (model.ServiceTag, model.BackupTypeTag) - those two drive
// merge/assist/preview base-selection logic elsewhere, so a caller forging
// one of those keys must never take effect.
func TestMergeAdditionalTags(t *testing.T) {
	table := []struct {
		name       string
		tags       map[string]string
		additional map[string]string
		expect     map[string]string
	}{
		{
			name:       "adds new keys",
			tags:       map[string]string{model.ServiceTag: "exchange"},
			additional: map[string]string{"tenant": "t1", "policy": "p1", "object": "o1"},
			expect: map[string]string{
				model.ServiceTag: "exchange",
				"tenant":         "t1",
				"policy":         "p1",
				"object":         "o1",
			},
		},
		{
			name: "never overrides an existing key",
			tags: map[string]string{
				model.ServiceTag:    "exchange",
				model.BackupTypeTag: model.MergeBackup,
			},
			additional: map[string]string{
				model.ServiceTag:    "forged-service",
				model.BackupTypeTag: "forged-type",
				"tenant":            "t1",
			},
			expect: map[string]string{
				model.ServiceTag:    "exchange",
				model.BackupTypeTag: model.MergeBackup,
				"tenant":            "t1",
			},
		},
		{
			name:       "nil additional is a no-op",
			tags:       map[string]string{model.ServiceTag: "exchange"},
			additional: nil,
			expect:     map[string]string{model.ServiceTag: "exchange"},
		},
		{
			name:       "empty tags gets populated",
			tags:       map[string]string{},
			additional: map[string]string{"tenant": "t1"},
			expect:     map[string]string{"tenant": "t1"},
		},
	}

	for _, test := range table {
		t.Run(test.name, func(t *testing.T) {
			mergeAdditionalTags(test.tags, test.additional)
			assert.Equal(t, test.expect, test.tags)
		})
	}
}
