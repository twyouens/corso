package engine

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/twyouens/corso/src/internal/model"
)

// TestValidateTags_RejectsReservedKeys proves a caller can never create a
// backup tagged with a key the fork's own bookkeeping ("service",
// "backup-type"), Kopia's model store ("type", "stableID",
// "storeVersion") or the fork's backup garbage collector ("tenant", and
// anything starting "sc-") reserves for itself through this package -
// this is the first of the two layers described in BackupRequest.Tags'
// doc comment; the second lives in internal/operations.mergeAdditionalTags.
//
// k3ep's own real tags use the "k3ep-" prefix (k3ep-tenant, k3ep-policy,
// k3ep-object) specifically so none of them can ever collide with a key
// or prefix reserved here.
func TestValidateTags_RejectsReservedKeys(t *testing.T) {
	table := []struct {
		name      string
		tags      map[string]string
		expectErr error
	}{
		{
			name: "nil tags is fine",
			tags: nil,
		},
		{
			name: "k3ep's own namespaced tags are fine",
			tags: map[string]string{"k3ep-tenant": "t1", "k3ep-policy": "p1", "k3ep-object": "o1"},
		},
		{
			name:      "colliding with service is rejected",
			tags:      map[string]string{model.ServiceTag: "forged"},
			expectErr: ErrReservedTagKey,
		},
		{
			name:      "colliding with backup-type is rejected",
			tags:      map[string]string{model.BackupTypeTag: "forged"},
			expectErr: ErrReservedTagKey,
		},
		{
			name:      "colliding with Kopia's manifest type label is rejected",
			tags:      map[string]string{"type": "forged"},
			expectErr: ErrReservedTagKey,
		},
		{
			name:      "colliding with Kopia's stableID is rejected",
			tags:      map[string]string{"stableID": "forged"},
			expectErr: ErrReservedTagKey,
		},
		{
			name:      "colliding with Kopia's storeVersion is rejected",
			tags:      map[string]string{"storeVersion": "forged"},
			expectErr: ErrReservedTagKey,
		},
		{
			name:      "colliding with the GC's tenant tag is rejected",
			tags:      map[string]string{"tenant": "t1"},
			expectErr: ErrReservedTagKey,
		},
		{
			name:      "using the GC's sc- prefix is rejected",
			tags:      map[string]string{"sc-exchange": "0"},
			expectErr: ErrReservedTagPrefix,
		},
		{
			name:      "sc- prefix is rejected even mid-key-set",
			tags:      map[string]string{"k3ep-tenant": "t1", "sc-onedrive": "0"},
			expectErr: ErrReservedTagPrefix,
		},
		{
			name:      "one reserved key among several others is still rejected",
			tags:      map[string]string{"k3ep-tenant": "t1", model.ServiceTag: "forged"},
			expectErr: ErrReservedTagKey,
		},
	}

	for _, test := range table {
		t.Run(test.name, func(t *testing.T) {
			err := validateTags(test.tags)

			if test.expectErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, test.expectErr)

				return
			}

			require.NoError(t, err)
		})
	}
}

// TestValidateTags_BoundsTagCountAndSize proves the DoS-shaped edges the
// security review named: an unbounded number of tags, or an unbounded key
// or value, land as Kopia manifest labels the manifest manager holds in
// memory for every Find call.
func TestValidateTags_BoundsTagCountAndSize(t *testing.T) {
	tagsOfCount := func(n int) map[string]string {
		tags := make(map[string]string, n)

		for i := 0; i < n; i++ {
			tags[fmt.Sprintf("k3ep-tag-%d", i)] = "v"
		}

		return tags
	}

	t.Run("at the tag count limit is fine", func(t *testing.T) {
		tags := tagsOfCount(maxTagCount)
		assert.NoError(t, validateTags(tags))
	})

	t.Run("one over the tag count limit is rejected", func(t *testing.T) {
		tags := tagsOfCount(maxTagCount + 1)

		err := validateTags(tags)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrTooManyTags)
	})

	t.Run("at the key length limit is fine", func(t *testing.T) {
		key := "k3ep-" + strings.Repeat("k", maxTagKeyBytes-len("k3ep-"))
		require.Len(t, key, maxTagKeyBytes)

		assert.NoError(t, validateTags(map[string]string{key: "v"}))
	})

	t.Run("one over the key length limit is rejected", func(t *testing.T) {
		key := "k3ep-" + strings.Repeat("k", maxTagKeyBytes-len("k3ep-")+1)
		require.Len(t, key, maxTagKeyBytes+1)

		err := validateTags(map[string]string{key: "v"})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrTagTooLarge)
	})

	t.Run("at the value length limit is fine", func(t *testing.T) {
		value := strings.Repeat("v", maxTagValueBytes)
		require.Len(t, value, maxTagValueBytes)

		assert.NoError(t, validateTags(map[string]string{"k3ep-key": value}))
	})

	t.Run("one over the value length limit is rejected", func(t *testing.T) {
		value := strings.Repeat("v", maxTagValueBytes+1)
		require.Len(t, value, maxTagValueBytes+1)

		err := validateTags(map[string]string{"k3ep-key": value})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrTagTooLarge)
	})

	t.Run("at the total bytes limit is fine", func(t *testing.T) {
		// Five tags, each comfortably within the per-key (256) and
		// per-value (1024) bounds, and five tags is nowhere near the
		// count bound (32) - but their combined bytes sit at or just
		// under maxTagTotalBytes, proving the total bound is checked,
		// not just implied by the per-tag ones.
		const n = 5

		tags := make(map[string]string, n)

		for i := 0; i < n; i++ {
			key := fmt.Sprintf("k3ep-k-%d", i) // fixed width: single-digit i
			valueLen := (maxTagTotalBytes / n) - len(key)
			tags[key] = strings.Repeat("v", valueLen)
		}

		total := 0
		for k, v := range tags {
			total += len(k) + len(v)
		}

		require.LessOrEqual(t, total, maxTagTotalBytes)
		assert.NoError(t, validateTags(tags))
	})

	t.Run("over the total bytes limit is rejected even though every tag is within bounds", func(t *testing.T) {
		// Same shape as above, but padded enough to push the combined
		// total past maxTagTotalBytes while every single key and value
		// stays far under maxTagKeyBytes/maxTagValueBytes, and the tag
		// count stays far under maxTagCount. Only the new total-bytes
		// bound can reject this.
		const n = 5

		key := "k3ep-key"
		valueLen := (maxTagTotalBytes / n) + 50
		require.Less(t, len(key), maxTagKeyBytes)
		require.Less(t, valueLen, maxTagValueBytes)

		value := strings.Repeat("v", valueLen)

		tags := make(map[string]string, n)
		for i := 0; i < n; i++ {
			tags[fmt.Sprintf("%s-%d", key, i)] = value
		}
		require.Less(t, len(tags), maxTagCount)

		total := 0
		for k, v := range tags {
			total += len(k) + len(v)
		}

		require.Greater(t, total, maxTagTotalBytes)

		err := validateTags(tags)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrTagsTooLarge)
	})

	t.Run("the adversarial extreme (1000 tags of 64KB) is rejected", func(t *testing.T) {
		tags := make(map[string]string, 1000)
		big := strings.Repeat("x", 64*1024)

		for i := 0; i < 1000; i++ {
			tags[fmt.Sprintf("k3ep-tag-%d", i)] = big
		}

		err := validateTags(tags)
		require.Error(t, err)
		assert.True(t,
			errors.Is(err, ErrTooManyTags) || errors.Is(err, ErrTagTooLarge),
			"expected ErrTooManyTags or ErrTagTooLarge, got %v", err)
	})
}
