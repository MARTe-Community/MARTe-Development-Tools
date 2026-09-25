package e2e

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/test/e2e/framework"
)

// `mdt check <directory>` must behave like `-P <directory>`: the sibling
// file defines the object referenced by the checked file.
func TestCheckDirectoryInputExpands(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()
	tf := framework.WrapT(t, ctx)

	tf.CreateFile("proj/a.marte", `#package t
+A = {
  Class = ReferenceContainer
  Ref = Bee
}
`)
	tf.CreateFile("proj/b.marte", `#package t
+Bee = {
  Class = ReferenceContainer
}
`)

	// Directory input: both files load, the cross-file reference resolves.
	dirResult := tf.RunCheck("proj")
	framework.AssertNoErrors(tf, dirResult)

	// Single file only: the reference is unresolved (proves the directory
	// scan above was what made it pass).
	fileResult := tf.RunCheck("proj/a.marte")
	framework.AssertErrors(tf, fileResult, "Bee")
}

// `-j vars.json` supplies variable overrides; `-v` still wins over it.
func TestJSONVarsFileOverride(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()
	tf := framework.WrapT(t, ctx)

	tf.CreateFile("main.marte", `#package t
var Streaming: bool = true
+App = {
  Class = ReferenceContainer
  #if @Streaming
    +Streaming_ = {
      Class = ReferenceContainer
    }
  #else
    +Local = { }
  #end
}
`)
	tf.CreateFile("vars.json", `{"Streaming": false}`)

	// Default: the Streaming branch is active, config is valid.
	framework.AssertNoErrors(tf, tf.RunCheck("main.marte"))

	// -j flips the switch: the else branch activates and is invalid
	// (an object without a Class field).
	framework.AssertErrors(tf, tf.RunCheck("-j", "vars.json", "main.marte"), "Class")

	// -v overrides -j, so the config is valid again.
	framework.AssertNoErrors(tf, tf.RunCheck("-j", "vars.json", "-vStreaming=true", "main.marte"))
}
