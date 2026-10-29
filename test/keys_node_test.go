package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/validator"
)

// keys(<reference path>) lists the children of a configuration node, so a
// condition can test whether a signal is declared in a DataSource.
func TestKeysOfConfigurationNode(t *testing.T) {
	out := buildTemp(t, `#package T

var names: [string] = { "A", "B", "C" }

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    +DDB = {
      Class = GAMDataSource
      Signals = {
        A = { Type = uint32 }
        B = { Type = uint32 }
      }
    }
  }
  +Functions = { Class = ReferenceContainer }
  +States = { Class = ReferenceContainer }
  +Scheduler = { Class = FastScheduler }
}

foreach n in @names
  if (@n in keys(App.Data.DDB.Signals))
    ("Found_" .. @n) = { Class = ReferenceContainer }
  end
end
`)
	for _, want := range []string{"Found_A = {", "Found_B = {"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Found_C") {
		t.Errorf("signal C is not declared but was accepted:\n%s", out)
	}
}

// A path relative to the enclosing package resolves through the lexical
// scope chain, like other references.
func TestKeysOfConfigurationNodeRelativePath(t *testing.T) {
	app := `#package T

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    +DDB = {
      Class = GAMDataSource
      Signals = {
        A = { Type = uint32 }
      }
    }
  }
  +Functions = { Class = ReferenceContainer }
  +States = { Class = ReferenceContainer }
  +Scheduler = { Class = FastScheduler }
}
`
	use := `#package T.App.Functions

var names: [string] = { "A", "Z" }

foreach n in @names
  if (@n in keys(Data.DDB.Signals))
    ("Hit_" .. @n) = { Class = ReferenceContainer }
  end
end
`
	out := buildFiles(t, app, use)
	if !strings.Contains(out, "Hit_A = {") {
		t.Errorf("relative path did not resolve:\n%s", out)
	}
	if strings.Contains(out, "Hit_Z") {
		t.Errorf("undeclared signal accepted:\n%s", out)
	}
}

// The validator evaluates the same conditions (mdt check), so a satisfied
// assert against a node's keys must not be reported, while an unsatisfied
// one must be.
func TestKeysOfConfigurationNodeInAssert(t *testing.T) {
	base := `#package T

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    +DDB = {
      Class = GAMDataSource
      Signals = {
        A = { Type = uint32 }
      }
    }
  }
  +Functions = { Class = ReferenceContainer }
  +States = { Class = ReferenceContainer }
  +Scheduler = { Class = FastScheduler }
}
`
	find := func(diags []validator.Diagnostic, msg string) bool {
		for _, d := range diags {
			if d.Level == validator.LevelError && strings.Contains(d.Message, msg) {
				return true
			}
		}
		return false
	}

	ok := validateTemp(t, base+"\nassert(\"A\" in keys(App.Data.DDB.Signals), \"missing A\")\n")
	if find(ok, "missing A") {
		t.Errorf("declared signal reported missing: %+v", ok)
	}

	bad := validateTemp(t, base+"\nassert(\"NOPE\" in keys(App.Data.DDB.Signals), \"missing NOPE\")\n")
	if !find(bad, "missing NOPE") {
		t.Errorf("undeclared signal not detected: %+v", bad)
	}
}
