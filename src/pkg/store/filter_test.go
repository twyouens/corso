package store

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/twyouens/corso/src/pkg/path"
)

// TestQueryFilters_Tags proves chunk 0.6's general-purpose tag filter: it
// narrows a backup listing by every key/value pair the caller supplies
// (k3ep's tenant/policy/object tags), it composes with the pre-existing
// Service filter, and - critically - calling it never mutates the map the
// caller passed in, so a caller can safely reuse or keep mutating their own
// map after the call.
func TestQueryFilters_Tags(t *testing.T) {
	table := []struct {
		name   string
		opts   []FilterOption
		expect map[string]string
	}{
		{
			name:   "no options leaves tags nil (match everything)",
			opts:   nil,
			expect: nil,
		},
		{
			name:   "Tags sets every key/value pair",
			opts:   []FilterOption{Tags(map[string]string{"tenant": "t1", "policy": "p1", "object": "o1"})},
			expect: map[string]string{"tenant": "t1", "policy": "p1", "object": "o1"},
		},
		{
			name:   "Tags composes with Service",
			opts:   []FilterOption{Service(path.ExchangeService), Tags(map[string]string{"tenant": "t1"})},
			expect: map[string]string{"service": "exchange", "tenant": "t1"},
		},
		{
			name:   "empty Tags map still initializes the filter (match nothing extra)",
			opts:   []FilterOption{Tags(map[string]string{})},
			expect: map[string]string{},
		},
	}

	for _, test := range table {
		t.Run(test.name, func(t *testing.T) {
			q := &queryFilters{}
			q.populate(test.opts...)
			assert.Equal(t, test.expect, q.tags)
		})
	}
}

// TestTags_DoesNotRetainCallerMap proves Tags copies the caller's map
// immediately, at call time: mutating the caller's map afterwards - even
// before the returned FilterOption is ever applied - has no effect on what
// the filter matches.
func TestTags_DoesNotRetainCallerMap(t *testing.T) {
	in := map[string]string{"tenant": "t1"}
	opt := Tags(in)

	in["tenant"] = "mutated-after-the-fact"

	q := &queryFilters{}
	q.populate(opt)

	assert.Equal(t, map[string]string{"tenant": "t1"}, q.tags)
}
