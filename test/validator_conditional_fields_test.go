package integration

import (
	"strings"
	"testing"
)

const conditionalFieldSrc = `#package T

var UseBadType: bool = false

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    DefaultDataSource = DDB
    +TDS = { Class = TimingDataSource }
    +DDB = {
      Class = GAMDataSource
      Signals = { X = { Type = uint32 } }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    +Use = {
      Class = FilterGAM
      InputSignals = {
        #if @UseBadType
        X = { DataSource = DDB  Type = bogusType }
        #else
        X = { DataSource = DDB  Type = uint32 }
        #end
      }
    }
  }
  Scheduler = { Class = GAMScheduler  TimingDataSource = TDS }
  +States = { Class = ReferenceContainer }
}
`

// TestConditionalBranchFieldsAreNotValidated covers `#if` blocks inside a
// subnode body: the index keeps the definitions of every branch so hover and
// completion work in all of them, but only the activated branch may be
// validated. Validating the aggregate reported errors for values the user
// never selected (and made `mdt check` reject the project).
func TestConditionalBranchFieldsAreNotValidated(t *testing.T) {
	t.Run("inactive branch is skipped", func(t *testing.T) {
		for _, d := range validateWithOverrides(t, conditionalFieldSrc, nil) {
			if strings.Contains(d.Message, "bogusType") {
				t.Errorf("inactive #if branch was validated: %s", d.Message)
			}
		}
	})

	t.Run("active branch is validated", func(t *testing.T) {
		diags := validateWithOverrides(t, conditionalFieldSrc, map[string]string{"UseBadType": "true"})
		for _, d := range diags {
			if strings.Contains(d.Message, "bogusType") {
				return
			}
		}
		t.Errorf("active #if branch was not validated, diagnostics: %+v", diags)
	})
}
