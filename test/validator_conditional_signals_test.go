package integration

import (
	"strings"
	"testing"
)

// conditionalGAMSignalsSrc is the shape reported from a real project: an
// IOGAM whose signal lists switch between two hardware variants. Each branch
// declares the same signal names against different data sources (and types),
// so the branch that is not selected must not reach validation at all.
const conditionalGAMSignalsSrc = `#package WG

var variant: string = "B"

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    DefaultDataSource = DDB6

    +DDB6 = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = {
        CCPS_REF = { Type = float32 }
        FHPS_SCALED = { Type = float32 }
        BPS_REF = { Type = int32 }
        APS_REF = { Type = int32 }
        FHPS_REF = { Type = float32 }
        FHPS_AUTO_STAT = { Type = uint32 }
      }
    }
    +Th1Bridge = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = {
        BPS_OUT = { Type = float32 }
        APS_OUT = { Type = float32 }
        MHVPS_OUT = { Type = float32 }
      }
    }
    +WGAsyncBridge = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = {
        CCPS_REF = { Type = float32 }
        FHPS_REF = { Type = float32 }
        FHPS_AUTO_STAT = { Type = uint32 }
      }
    }
    +NI6259_AO = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = {
        CCPS_REF = { Type = float32 }
        FHPS_REF = { Type = float32 }
      }
    }
    +NI6368_AO = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = {
        BPS_VREF = { Type = float32 }
        APS_VREF = { Type = float32 }
        MAIN_VREF = { Type = float32 }
      }
    }
    +APSBPS_CRIO = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = {
        BPS_VREF = { Type = int32 }
        APS_VREF = { Type = int32 }
      }
    }
  }

  +Functions = {
    Class = ReferenceContainer
    +WGProducerGAM = {
      Class = IOGAM
      InputSignals = {
        DDB6::CCPS_REF: float32 as CCPS_REF_MON
        DDB6::CCPS_REF: float32
        DDB6::FHPS_SCALED: float32 as FHPS_REF
        if (@variant == "A")
          Th1Bridge::BPS_OUT: float32
          Th1Bridge::APS_OUT: float32
        else
          DDB6::BPS_REF: int32
          DDB6::APS_REF: int32
        end
        Th1Bridge::MHVPS_OUT: float32 as MAIN_REF
        DDB6::FHPS_REF: float32 as FHPS_REF_MON
        DDB6::FHPS_AUTO_STAT: uint32 as FHPS_STATE_MON
      }
      OutputSignals = {
        WGAsyncBridge::CCPS_REF: float32 as CCPS_REF_MON
        NI6259_AO::CCPS_REF: float32
        NI6259_AO::FHPS_REF: float32
        if (@variant == "A")
          NI6368_AO::BPS_VREF: float32
          NI6368_AO::APS_VREF: float32
        else
          APSBPS_CRIO::BPS_VREF: int32
          APSBPS_CRIO::APS_VREF: int32
        end
        NI6368_AO::MAIN_VREF: float32
        WGAsyncBridge::FHPS_REF: float32 as FHPS_REF_MON
        WGAsyncBridge::FHPS_AUTO_STAT: uint32 as FHPS_STATE_MON
      }
    }
  }

  Scheduler = { Class = GAMScheduler  TimingDataSource = DDB6 }
  +States = { Class = ReferenceContainer }
}
`

// TestConditionalGAMSignalBranches covers signal shorthands inside an
// `if`/`else` in a GAM signal list. Indexing keeps both branches, but
// validation must use only the selected one: otherwise the IOGAM byte-size
// rule sees both branches, the shared signal nodes mix the branches' types,
// and the data source of the inactive branch is reported as unused.
func TestConditionalGAMSignalBranches(t *testing.T) {
	check := func(t *testing.T, variant, usedDS, unusedDS string) {
		t.Helper()
		diags := validateWithOverrides(t, conditionalGAMSignalsSrc, map[string]string{"variant": `"` + variant + `"`})
		for _, d := range diags {
			switch {
			case strings.Contains(d.Message, "InputSize"):
				t.Errorf("IOGAM byte size counted both branches: %s", d.Message)
			case strings.Contains(d.Message, "property 'Type' mismatch"):
				t.Errorf("signal type taken from the inactive branch: %s", d.Message)
			case strings.Contains(d.Message, "Unused Signal") && strings.Contains(d.Message, unusedDS):
				// The data source of the inactive branch really is unused;
				// anything else is covered by the assertion below.
			case strings.Contains(d.Message, "Unused Signal") && strings.Contains(d.Message, usedDS):
				t.Errorf("data source used by the selected branch reported as unused: %s", d.Message)
			}
		}
	}

	t.Run("variant A uses the bridge signals", func(t *testing.T) {
		check(t, "A", "NI6368_AO", "APSBPS_CRIO")
	})

	t.Run("variant B uses the cRIO signals", func(t *testing.T) {
		check(t, "B", "APSBPS_CRIO", "NI6368_AO")
	})
}

// TestConditionalGAMSignalBuildAndValidate keeps the build output and the
// diagnostics in sync: only the selected branch's signals may be emitted
// (otherwise the IOGAM's input/output lists are duplicated).
func TestConditionalGAMSignalBuildAndValidate(t *testing.T) {
	// `DataSource = X` appears only on GAM signal entries, so counting them
	// ignores the data source declarations themselves.
	out := buildTemp(t, conditionalGAMSignalsSrc)
	if n := strings.Count(out, "DataSource = Th1Bridge"); n != 1 { // MHVPS_OUT only
		t.Errorf("build emitted %d bridge inputs, want 1 (the #if branch is inactive):\n%s", n, out)
	}
	if n := strings.Count(out, "DataSource = APSBPS_CRIO"); n != 2 {
		t.Errorf("build emitted %d cRIO outputs, want 2 (the #else branch is selected):\n%s", n, out)
	}
}
