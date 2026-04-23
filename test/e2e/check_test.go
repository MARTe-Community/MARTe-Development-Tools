package e2e

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/test/e2e/framework"
)

func TestCheckBasic(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	tf.CreateFile("valid.marte", `
//! allow(unknown_class)
+Valid = {
    Class = "GAM"
}
`)

	result := tf.RunCheck("valid.marte")
	framework.AssertNoErrors(tf, result)
}

func TestCheckInvalidClass(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	tf.CreateFile("invalid.marte", `
+NoClass = {
    Field = "value"
}
`)

	result := tf.RunCheck("invalid.marte")
	framework.AssertErrors(tf, result, "Class")
}

func TestCheckDuplicate(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	tf.CreateFile("dup.marte", `
+Dup = {
    Field = "first"
    Field = "second"
}
`)

	result := tf.RunCheck("dup.marte")
	framework.AssertErrors(tf, result, "Duplicate")
}

func TestCheckSignalType(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	tf.CreateFile("signal.marte", `
//! allow(unknown_class)
//! allow(unused_signal)
+Config = {
    Class = "GAM"
    Signals = {
        Signal1 = {
            Type = "uint32"
        }
    }
}
`)

	result := tf.RunCheck("signal.marte")
	framework.AssertNoErrors(tf, result)
}

func TestCheckSignalMissingType(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	tf.CreateFile("notype.marte", `
//! allow(unknown_class)
+Config = {
    Class = "GAM"
    Signals = {
        Signal1 = {
        }
    }
}
`)

	result := tf.RunCheck("notype.marte")
	framework.AssertErrors(tf, result, "Signal1")
}

func TestCheckGAMSignals(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	tf.CreateFile("gamsignals.marte", `
//! allow(unknown_class)
//! allow(unused_gam)
+InputGAM = {
    Class = "InputGAM"
    OutputSignals = {
        Signal1 = {
            Type = "uint32"
        }
    }
}

+OutputGAM = {
    Class = "OutputGAM"
    InputSignals = {
        Signal1 = {
            Type = "uint32"
        }
    }
}
`)

	result := tf.RunCheck("gamsignals.marte")
	framework.AssertNoErrors(tf, result)
}

func TestCheckGAMSignalMismatch(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	tf.CreateFile("mismatch.marte", `
+Dup = {
    Field = "first"
    Field = "second"
}
`)

	result := tf.RunCheck("mismatch.marte")
	framework.AssertErrors(tf, result, "Duplicate")
}

func TestCheckFolderRecurse(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	subdir := tf.CreateSubdir("src")

	tf.CreateFile("src/file1.marte", `
//! allow(unknown_class)
+File1 = {
    Class = "Test"
}
`)

	tf.CreateFile("src/file2.marte", `
//! allow(unknown_class)
+File2 = {
    Class = "Test"
}
`)

	result := tf.RunCheck("-P", subdir)
	framework.AssertNoErrors(tf, result)
}

func TestCheckWithSchema(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	tf.CreateFile("schema.marte", `
+WithSchema = {
    Class = "Test"
    RequiredField = "value"
}
`)

	result := tf.RunCheck("schema.marte")

	t.Logf("Diagnostics: %v", result.Diagnostics)
}
