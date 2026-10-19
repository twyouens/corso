package tester_test

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/twyouens/corso/src/internal/tester"
)

// recordingT is a require.TestingT that records a failure instead of
// failing the real test, so the helpers' own failure path can be asserted.
// FailNow ends the goroutine it runs on, as testing.T's does.
type recordingT struct {
	failed bool
	msgs   []string
}

func (r *recordingT) Errorf(format string, args ...any) {
	r.msgs = append(r.msgs, fmt.Sprintf(format, args...))
}

func (r *recordingT) FailNow() {
	r.failed = true
	runtime.Goexit()
}

// runRecorded runs fn against a fresh recordingT on its own goroutine, so a
// FailNow inside fn stops fn and nothing else.
func runRecorded(fn func(t *recordingT)) *recordingT {
	r := &recordingT{}
	done := make(chan struct{})

	go func() {
		defer close(done)
		fn(r)
	}()

	<-done

	return r
}

const (
	envSetVar   = "CORSO_TESTER_ENVVARS_SET"
	envEmptyVar = "CORSO_TESTER_ENVVARS_EMPTY"
	envUnsetVar = "CORSO_TESTER_ENVVARS_UNSET"
	envSetValue = "present-value"
)

// setEnvs gives the three fixture variables their states for this test:
// one set, one set to the empty string, and one not set at all.
func setEnvs(t *testing.T) {
	t.Setenv(envSetVar, envSetValue)
	t.Setenv(envEmptyVar, "")
	// Setenv first so the variable's prior state is restored afterwards.
	t.Setenv(envUnsetVar, "")
	require.NoError(t, os.Unsetenv(envUnsetVar))
}

// TestMustGetEnvVars_RequiresAValue checks the value of each named
// variable, not its name. The name is never empty, so a check of the name
// passes every time, and a suite gated on a variable that is unset or empty
// ran anyway with a blank value instead of failing with that variable's
// name - the bug this test pins.
func TestMustGetEnvVars_RequiresAValue(t *testing.T) {
	setEnvs(t)

	table := []struct {
		name       string
		vars       []string
		expectFail string // the variable the failure must name; "" for success
	}{
		{name: "set", vars: []string{envSetVar}},
		{name: "empty", vars: []string{envEmptyVar}, expectFail: envEmptyVar},
		{name: "unset", vars: []string{envUnsetVar}, expectFail: envUnsetVar},
		{name: "set then unset", vars: []string{envSetVar, envUnsetVar}, expectFail: envUnsetVar},
	}
	for _, test := range table {
		t.Run(test.name, func(t *testing.T) {
			var got map[string]string

			rt := runRecorded(func(rt *recordingT) {
				got = tester.MustGetEnvVars(rt, test.vars...)
			})

			if len(test.expectFail) == 0 {
				assert.False(t, rt.failed, "no failure expected; got %v", rt.msgs)
				assert.Equal(t, map[string]string{envSetVar: envSetValue}, got)

				return
			}

			assert.True(t, rt.failed, "a missing value must fail the suite")
			assert.Contains(t, strings.Join(rt.msgs, "\n"), test.expectFail, "the failure names the variable")
		})
	}
}

// TestMustGetEnvSets_RequiresAValue checks the same through the set form
// every gated suite constructor uses.
func TestMustGetEnvSets_RequiresAValue(t *testing.T) {
	setEnvs(t)

	rt := runRecorded(func(rt *recordingT) {
		tester.MustGetEnvSets(rt, []string{envSetVar}, []string{envEmptyVar})
	})

	assert.True(t, rt.failed, "an empty variable in any set must fail the suite")
	assert.Contains(t, strings.Join(rt.msgs, "\n"), envEmptyVar, "the failure names the variable")

	var got map[string]string

	rt = runRecorded(func(rt *recordingT) {
		got = tester.MustGetEnvSets(rt, []string{envSetVar})
	})

	assert.False(t, rt.failed, "no failure expected; got %v", rt.msgs)
	assert.Equal(t, map[string]string{envSetVar: envSetValue}, got)
}
