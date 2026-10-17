package seed

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/twyouens/corso/src/internal/tester"
)

type WorkloadSummaryUnitSuite struct {
	tester.Suite
}

func TestWorkloadSummaryUnitSuite(t *testing.T) {
	suite.Run(t, &WorkloadSummaryUnitSuite{Suite: tester.NewUnitSuite(t)})
}

func (suite *WorkloadSummaryUnitSuite) TestRecord_TalliesFoundAndCreated() {
	t := suite.T()

	s := WorkloadSummary{Workload: WorkloadOneDrive}
	s.record(true)  // created
	s.record(false) // found
	s.record(false) // found

	assert.Equal(t, 1, s.Created)
	assert.Equal(t, 2, s.Found)
}

func (suite *WorkloadSummaryUnitSuite) TestString_NamesWorkloadAndCounts() {
	t := suite.T()

	s := WorkloadSummary{Workload: WorkloadSharePointList, Found: 2, Created: 0}

	// The point of this check: the line names only the workload and the
	// counts, never an item name, subject, filename or credential.
	assert.Equal(t, "sharepoint-list: 2 found, 0 created", s.String())
}
