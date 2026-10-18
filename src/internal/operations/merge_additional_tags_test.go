package operations

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/twyouens/corso/src/internal/model"
)

// TestMergeAdditionalTags proves chunk 0.6's rule that a caller-supplied
// backup tag (pkg/engine.BackupRequest.Tags, threaded through
// BackupOperation.AdditionalTags) can never collide with, and silently
// overwrite or introduce, a tag reserved by this fork's own bookkeeping
// (model.ServiceTag, model.BackupTypeTag), Kopia's model store ("type",
// "stableID", "storeVersion"), or this fork's backup garbage collector
// ("tenant", and anything starting "sc-"). The first two drive
// merge/assist/preview base-selection logic elsewhere; the rest are
// listed in reservedAdditionalTagKeys' own doc comment. A caller forging
// any of them must never take effect, whether or not createBackupModels
// already set that exact key by the time merge runs.
func TestMergeAdditionalTags(t *testing.T) {
	table := []struct {
		name       string
		tags       map[string]string
		additional map[string]string
		expect     map[string]string
	}{
		{
			name:       "adds new, non-reserved keys",
			tags:       map[string]string{model.ServiceTag: "exchange"},
			additional: map[string]string{"k3ep-tenant": "t1", "k3ep-policy": "p1", "k3ep-object": "o1"},
			expect: map[string]string{
				model.ServiceTag: "exchange",
				"k3ep-tenant":    "t1",
				"k3ep-policy":    "p1",
				"k3ep-object":    "o1",
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
				"k3ep-tenant":       "t1",
			},
			expect: map[string]string{
				model.ServiceTag:    "exchange",
				model.BackupTypeTag: model.MergeBackup,
				"k3ep-tenant":       "t1",
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
			additional: map[string]string{"k3ep-tenant": "t1"},
			expect:     map[string]string{"k3ep-tenant": "t1"},
		},
		{
			// These five keys are not pre-populated in tags by
			// createBackupModels, unlike service/backup-type - this is
			// exactly the gap an earlier version of mergeAdditionalTags
			// (and its doc comment) missed: "never overrides an existing
			// key" protects nothing for a key that was never there to
			// begin with.
			name: "rejects Kopia- and GC-reserved keys even when not already present",
			tags: map[string]string{model.ServiceTag: "exchange"},
			additional: map[string]string{
				"type":         "forged",
				"stableID":     "forged",
				"storeVersion": "forged",
				"tenant":       "forged",
				"sc-exchange":  "forged",
				"k3ep-object":  "o1",
			},
			expect: map[string]string{
				model.ServiceTag: "exchange",
				"k3ep-object":    "o1",
			},
		},
	}

	for _, test := range table {
		t.Run(test.name, func(t *testing.T) {
			mergeAdditionalTags(test.tags, test.additional)
			assert.Equal(t, test.expect, test.tags)
		})
	}
}
