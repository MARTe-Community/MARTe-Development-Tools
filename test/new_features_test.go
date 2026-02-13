package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
)

func TestNewFeatures(t *testing.T) {
	content := `
	#package Test
	#var EnableFeature: bool = true
	#var Elements: array = { 1 2 3 }
	
	#template MyTemplate(ID: int, Type: string = "Default")
		"+Instance_" .. @ID = {
			Class = "TemplateClass"
			Type = @Type
			#if @ID > 1
				ExtraField = "Greater than 1"
			#else
				ExtraField = "1 or less"
			#end
		}
	#end

	+Root = {
		Class = "RootClass"
		
		#if $EnableFeature
			FeatureField = "Enabled"
		#else
			FeatureField = "Disabled"
		#end

		#foreach Val in $Elements
			"+Item_" .. @Val = {
				Value = @Val
			}
		#end

		#use MyTemplate Instance10 (ID = 10, Type = "Special")
		#use MyTemplate Instance1 (ID = 1)
	}
	`

	tmpFile := "test_features.marte"
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile)

	b := builder.NewBuilder([]string{tmpFile}, nil)
	
	out, err := os.CreateTemp("", "out*.marte")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(out.Name())

	err = b.Build(out)
	if err != nil {
		t.Fatal(err)
	}

	outContent, _ := os.ReadFile(out.Name())
	res := string(outContent)

	t.Logf("Generated output:\n%s", res)

	// Verification
	if !strings.Contains(res, "FeatureField = \"Enabled\"") {
		t.Error("Expected FeatureField = \"Enabled\"")
	}
	if !strings.Contains(res, "\"+Item_1\" = {") || !strings.Contains(res, "\"+Item_2\" = {") || !strings.Contains(res, "\"+Item_3\" = {") {
		t.Error("Expected items 1, 2, 3 from foreach")
	}
	// Template instances are nested under the instance name provided in #use
	if !strings.Contains(res, "Instance10 = {") || !strings.Contains(res, "\"+Instance_10\" = {") || !strings.Contains(res, "Type = \"Special\"") {
		t.Error("Expected template instance 10 with Special type")
	}
	if !strings.Contains(res, "Instance1 = {") || !strings.Contains(res, "\"+Instance_1\" = {") || !strings.Contains(res, "Type = \"Default\"") {
		t.Error("Expected template instance 1 with Default type")
	}
	if !strings.Contains(res, "ExtraField = \"Greater than 1\"") {
		t.Error("Expected ExtraField = \"Greater than 1\" for instance 10")
	}
	if !strings.Contains(res, "ExtraField = \"1 or less\"") {
		t.Error("Expected ExtraField = \"1 or less\" for instance 1")
	}
}
