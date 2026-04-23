package e2e

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/test/e2e/framework"
)

func TestBuildBasic(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	tf.CreateFile("config.marte", `
+MyConfig = {
    Class = "Test"
    Value = 123
}
`)

	result := tf.RunBuild("config.marte")

	if result.ExitCode != 0 {
		t.Fatalf("Build failed: %s", result.Stderr)
	}

	if !strings.Contains(result.Output, "MyConfig") {
		t.Fatalf("Expected output to contain MyConfig, got: '%s'", result.Output)
	}
}

func TestBuildMultiFile(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	tf.CreateFile("base.marte", `
//! allow(unknown_class)
+Base = {
    Class = "BaseClass"
}
`)

	tf.CreateFile("derived.marte", `
//! allow(unknown_class)
+Derived = {
    Class = "DerivedClass"
    SomeField = "value"
}
`)

	result := tf.RunBuild("base.marte", "derived.marte")

	if result.ExitCode != 0 {
		t.Fatalf("Build failed: %s", result.Stderr)
	}

	output := result.Output
	if !strings.Contains(output, "Base") {
		t.Fatalf("Expected output to contain Base")
	}
	if !strings.Contains(output, "Derived") {
		t.Fatalf("Expected output to contain Derived")
	}
}

func TestBuildWithVariables(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	tf.CreateFile("config.marte", `
//! allow(unknown_class)
#let ENVIRONMENT: string = "production"

+MyConfig = {
    Class = "Test"
    Env = @ENVIRONMENT
}
`)

	result := tf.RunBuild("config.marte")

	if result.ExitCode != 0 {
		t.Fatalf("Build failed: %s", result.Stderr)
	}
}

func TestBuildOutputFile(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	tf.CreateFile("config.marte", `
+Output = {
    Class = "Test"
}
`)

	outputPath := tf.CreateFile("output.txt", "")

	result := tf.RunBuild("-o", outputPath, "config.marte")

	if result.ExitCode != 0 {
		t.Fatalf("Build failed: %s", result.Stderr)
	}

	content, err := tf.ReadFile("output.txt")
	if err != nil {
		t.Fatalf("Failed to read output file: %v", err)
	}

	if !strings.Contains(content, "Output") {
		t.Fatalf("Expected output file to contain Output")
	}
}

func TestCheckValidConfig(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	tf.CreateFile("valid.marte", `
//! allow(unknown_class)
//! allow(unused_gam)
+ValidConfig = {
    Class = "GAM"
    InputSignals = {
        Signal1 = {
            Type = "uint32"
        }
    }
}
`)

	result := tf.RunCheck("valid.marte")

	if len(result.Diagnostics) > 0 {
		t.Fatalf("Expected no diagnostics for valid config, got: %v", result.Diagnostics)
	}
}

func TestCheckDuplicateField(t *testing.T) {
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

func TestCheckMissingClass(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	tf.CreateFile("noclass.marte", `
+NoClass = {
    Value = 123
}
`)

	result := tf.RunCheck("noclass.marte")

	if len(result.Diagnostics) == 0 {
		t.Fatalf("Expected missing Class error")
	}
}

func TestCheckFolder(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	subdir := tf.CreateSubdir("configs")

	tf.CreateFile("configs/valid1.marte", `
//! allow(unknown_class)
+Valid1 = {
    Class = "Test"
}
`)

	tf.CreateFile("configs/valid2.marte", `
//! allow(unknown_class)
+Valid2 = {
    Class = "Test"
}
`)

	result := tf.RunCheck("-P", subdir)

	if len(result.Diagnostics) > 0 {
		t.Fatalf("Expected no diagnostics for valid configs, got: %v", result.Diagnostics)
	}
}

func TestBuildWithProjectFilter(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	tf.CreateFile("proj1.marte", `
#package proj1
//! allow(unknown_class)
+Proj1Config = {
    Class = "Test"
}
`)

	tf.CreateFile("proj2.marte", `
#package proj2
//! allow(unknown_class)
+Proj2Config = {
    Class = "Test"
}
`)

	result := tf.RunBuild("-p", "proj1", "proj1.marte", "proj2.marte")

	if result.ExitCode != 0 {
		t.Fatalf("Build failed: %s", result.Stderr)
	}

	if !strings.Contains(result.Output, "Proj1Config") {
		t.Fatalf("Expected output to contain Proj1Config")
	}

	if strings.Contains(result.Output, "Proj2Config") {
		t.Fatalf("Expected output to NOT contain Proj2Config (filtered)")
	}
}

func TestBuildWithVariableOverride(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	tf.CreateFile("config.marte", `
//! allow(unknown_class)
#var COUNT: int = 5

+Config = {
    Class = "Test"
    Count = @COUNT
}
`)

	result := tf.RunBuild("-vCOUNT=99", "config.marte")

	if result.ExitCode != 0 {
		t.Fatalf("Build failed: %s", result.Stderr)
	}

	if !strings.Contains(result.Output, "99") {
		t.Fatalf("Expected output to contain overridden value '99'")
	}
}

func TestBuildWithConditional(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	tf.CreateFile("config.marte", `
//! allow(unknown_class)
#var ENABLE_FEATURE: bool = true

+Config = {
    Class = "Test"
    #if @ENABLE_FEATURE
    Feature = {
        Enabled = true
    }
    #end
}
`)

	result := tf.RunBuild("config.marte")

	if result.ExitCode != 0 {
		t.Fatalf("Build failed: %s", result.Stderr)
	}

	if !strings.Contains(result.Output, "Feature") {
		t.Fatalf("Expected output to contain Feature")
	}
}

func TestBuildWithLoop(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	tf.CreateFile("config.marte", `
//! allow(unknown_class)
#foreach name in { "Signal1", "Signal2", "Signal3" }
("+Test" .. @name) = {
    Class = "Test"
}
#end
`)

	result := tf.RunBuild("config.marte")

	if result.ExitCode != 0 {
		t.Fatalf("Build failed: %s", result.Stderr)
	}

	if !strings.Contains(result.Output, "Signal1") {
		t.Fatalf("Expected output to contain Signal1")
	}
	if !strings.Contains(result.Output, "Signal2") {
		t.Fatalf("Expected output to contain Signal2")
	}
	if !strings.Contains(result.Output, "Signal3") {
		t.Fatalf("Expected output to contain Signal3")
	}
}
