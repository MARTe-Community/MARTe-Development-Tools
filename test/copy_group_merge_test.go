package integration

import (
	"strings"
	"testing"
)

// TestCopyGroupsSameNameMerge pins that several `copy Links { … }` blocks in
// different places describe one IOGAM: each block contributes items instead of
// emitting a duplicate object definition (which failed on the repeated Class
// field).
func TestCopyGroupsSameNameMerge(t *testing.T) {
	out := buildTemp(t, `#package T

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    DefaultDataSource = SRC
    +SRC = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = {
        X = { Type = float32 }
        Z = { Type = float32 }
      }
    }
    +DST = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = {
        Y = { Type = float32 }
        W = { Type = float32 }
      }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    +Gen = {
      Class = ConstantGAM
      OutputSignals = {
        X = { DataSource = SRC  Type = float32 }
        Z = { DataSource = SRC  Type = float32 }
      }
    }
    copy Links {
      SRC::X -> DST::Y
    }
    copy Links {
      SRC::Z -> DST::W
    }
  }
  Scheduler = { Class = GAMScheduler  TimingDataSource = SRC }
  +States = {
    Class = ReferenceContainer
    +Run = { Class = RealTimeState  +Th = { Class = RealTimeThread  CPUs = 0x1  Functions = { Gen Links } } }
  }
}
`)
	if n := strings.Count(out, "+Links = {"); n != 1 {
		t.Fatalf("expected one merged Links IOGAM, found %d:\n%s", n, out)
	}
	for _, want := range []string{"X = {", "Z = {", "Y = {", "W = {"} {
		if !strings.Contains(out, want) {
			t.Errorf("merged copy missing %q:\n%s", want, out)
		}
	}
}

// TestCopyGroupsSameNameInFunctionsArray: two group elements with the same
// name in a Functions list merge into one IOGAM and one reference.
func TestCopyGroupsSameNameInFunctionsArray(t *testing.T) {
	out := buildTemp(t, `#package T

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    DefaultDataSource = SRC
    +SRC = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = {
        X = { Type = float32 }
        Z = { Type = float32 }
      }
    }
    +DST = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = {
        Y = { Type = float32 }
        W = { Type = float32 }
      }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    +Gen = {
      Class = ConstantGAM
      OutputSignals = {
        X = { DataSource = SRC  Type = float32 }
        Z = { DataSource = SRC  Type = float32 }
      }
    }
  }
  Scheduler = { Class = GAMScheduler  TimingDataSource = SRC }
  +States = {
    Class = ReferenceContainer
    +Run = {
      Class = RealTimeState
      +Th = {
        Class = RealTimeThread
        CPUs = 0x1
        Functions = { Gen copy Links { SRC::X -> DST::Y } copy Links { SRC::Z -> DST::W } }
      }
    }
  }
}
`)
	if n := strings.Count(out, "+Links = {"); n != 1 {
		t.Fatalf("expected one merged Links IOGAM, found %d:\n%s", n, out)
	}
	if n := strings.Count(out, "Functions = { Gen Links }"); n != 1 {
		t.Errorf("expected a single Links reference in the thread, found %d:\n%s", n, out)
	}
}

// TestLoopScopedDataSourcesKeepEveryIteration covers two same-named
// DataSources declared inside a foreach: every iteration's signal must land in
// them (the second iteration used to be skipped, which made copies report the
// signals as undefined).
func TestLoopScopedDataSourcesKeepEveryIteration(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "signals.json", `{"AnalogInputSignals":[{"name":"AI0","channel":1},{"name":"AI1","channel":4}]}`)
	src := `#package T

var samples_adc: uint32 = 100

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    DefaultDataSource = DDB4
    with json("signals.json") as signals begin
      foreach id, signal in enumerate(@signals.AnalogInputSignals)
        +FastADCDataSource = {
          Class = GAMDataSource
          AllowNoProducers = 1
          Signals = {
            @signal.name = {
              Type = float32
              ChannelId = @id
            }
          }
        }
        +DDB4 = {
          Class = GAMDataSource
          AllowNoProducers = 1
          Signals = {
            @signal.name = {
              Type = float32
              NumberOfElements = @samples_adc
            }
          }
        }
      end
    end
  }
  +Functions = {
    Class = ReferenceContainer
    +Gen = {
      Class = ConstantGAM
      OutputSignals = {
        AI0 = { DataSource = FastADCDataSource  Type = float32 }
        AI1 = { DataSource = FastADCDataSource  Type = float32 }
      }
    }
    with json("signals.json") as signals begin
      foreach signal in @signals.AnalogInputSignals
        copy ReadADC {
          FastADCDataSource::@signal.name { Samples = @samples_adc } -> DDB4::@signal.name
        }
      end
    end
  }
  Scheduler = { Class = GAMScheduler  TimingDataSource = DDB4 }
  +States = {
    Class = ReferenceContainer
    +Run = { Class = RealTimeState  +Th = { Class = RealTimeThread  CPUs = 0x1  Functions = { Gen ReadADC } } }
  }
}
`
	file := writeFile(t, dir, "app.marte", src)
	out := buildTempWithDir(t, dir, file)

	// Both iterations declare their signal in both DataSources.
	for _, want := range []string{"ChannelId = 0", "ChannelId = 1", "NumberOfElements = 100"} {
		if !strings.Contains(out, want) {
			t.Errorf("build output missing %q:\n%s", want, out)
		}
	}
	// No signal may be reported as implicit: every copy endpoint resolves
	// against a declared DataSource signal.
	diags := validateWithOverrides(t, src, nil)
	for _, d := range diags {
		if strings.Contains(d.Message, "Implicitly Defined Signal") || strings.Contains(d.Message, "must define Type") {
			t.Errorf("unresolved signal diagnostic: %s", d.Message)
		}
	}
}
