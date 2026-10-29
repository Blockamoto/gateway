package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBitmapUnbuiltCustomProfilesAppearInRealStatusAndTimeline(t *testing.T) {
	a, _, _ := timelineFixture(t, 1)
	district := bitmapTestCreateProfile(t, a.dataDir, "Own-body comparison", 0, true)
	instances, err := bitmapProfileInstances(context.Background(), a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	// A nonexecutable draft must not become a track merely because its settings
	// have been saved alongside executable interpretations.
	rules := bitmapReferenceAnswers("district")
	rules["burns"] = 1
	if _, err = bitmapSaveStoredProfile(context.Background(), a.dataDir, bitmapProfileRequest{Action: "save_draft", Kind: "district", Label: "Unimplemented burn policy", Rules: rules}); err != nil {
		t.Fatal(err)
	}
	status := a.indexStatus()
	definitions := map[string]indexDefinition{}
	for _, d := range status.Definitions {
		if _, duplicate := definitions[d.ID]; duplicate {
			t.Fatal("duplicate runtime track", d.ID)
		}
		definitions[d.ID] = d
		if strings.HasPrefix(d.ID, "bitmap-draft-") {
			t.Fatal("nonexecutable draft became a timeline track", d)
		}
	}
	custom := 0
	for _, i := range instances {
		if !bitmapCustomConsumerID(i.ID) {
			continue
		}
		custom++
		d, ok := definitions[i.ID]
		if !ok || d.Name != i.Label || d.Kind != i.Kind || d.ProfileHash != i.ProfileHash || d.RuleHash != i.ProfileHash || d.ConfigurationRevision != bitmapEffectiveConfigurationRevision(i) || d.Classification != bitmapProfileClassification(i) || d.Buildable || !d.Locked || d.LockReason == "" || d.StartHeight != i.StartHeight || d.DistrictInstance != i.DistrictInstance || d.DistrictProfileHash != i.DistrictProfileHash || d.ChainInstance != i.ChainInstance {
			t.Fatal("unbuilt custom interpretation missing or mislabeled", i, d)
		}
		template, err := findIndexDefinition(i.ID)
		if err != nil || d.RecipeHash != template.RuleHash {
			t.Fatal("recipe identity confused with semantic profile", d, template, err)
		}
		wantDependencies := []string{"blocks"}
		if i.Kind == "parcel" {
			wantDependencies = []string{district.ID, "blocks"}
		} else if i.Kind == "chain" {
			wantDependencies = []string{district.ID}
		}
		if !reflect.DeepEqual(d.Dependencies, wantDependencies) {
			t.Fatal("custom track depends on a different District", d)
		}
		track := timelineTrack(t, status.Timeline, i.ID)
		if !track.Locked || len(track.Coverage) != 0 || len(track.RecordedCoverage) != 0 || track.PotentialFrom != i.StartHeight {
			t.Fatal("unbuilt profile invented saved coverage", track)
		}
		for _, built := range status.Instances {
			if built.Definition == i.ID {
				t.Fatal("unbuilt profile advertised a checkpoint", built)
			}
		}
		if _, err := os.Stat(bitmapDevelopmentDir(a.dataDir, i.ID)); !os.IsNotExist(err) {
			t.Fatal("status created a derived consumer store", i.ID, err)
		}
	}
	if custom != 3 {
		t.Fatal("District, linked Parcel and Chain must all be visible", custom)
	}
	encoded, err := json.Marshal(definitions[district.ID])
	var wire map[string]any
	if err != nil || json.Unmarshal(encoded, &wire) != nil || wire["profile_hash"] != district.ProfileHash || wire["kind"] != "district" || wire["chain_instance"] != district.ChainInstance {
		t.Fatal("workspace metadata absent from real status JSON", string(encoded), err)
	}
	if !strings.Contains(strings.Join(definitions[district.ID].Rules, " "), "own-body-ascii-ignore-media-v1") {
		t.Fatal("custom body policy replaced by the fixed reference recipe")
	}
	if a.indexCancel != nil || a.indexJob.ID != "" || status.Run.Run != nil || status.Run.Configured || len(status.Jobs) != 0 {
		t.Fatal("reading custom tracks started work", status.Run, status.Jobs)
	}
	for _, name := range []string{"run.json", "job.json", "queue.json"} {
		if _, err := os.Stat(filepath.Join(a.dataDir, "indexes", name)); !os.IsNotExist(err) {
			t.Fatal("custom status persisted an index job", name, err)
		}
	}
	// A metadata rename changes the display after refresh, never its executable R.
	if _, err := bitmapSaveStoredProfile(context.Background(), a.dataDir, bitmapProfileRequest{Action: "rename", ID: district.ID, Label: "Renamed comparison"}); err != nil {
		t.Fatal(err)
	}
	for _, d := range a.indexStatus().Definitions {
		if d.ID == district.ID && (d.Name != "Renamed comparison" || d.ProfileHash != district.ProfileHash) {
			t.Fatal("refresh lost immutable profile identity", d)
		}
	}
}

func TestBitmapCustomStatusReportsDamagedRegistryWithoutDroppingBuiltins(t *testing.T) {
	a, _, _ := timelineFixture(t, 1)
	i := bitmapTestCreateProfile(t, a.dataDir, "Saved custom track", 1, false)
	if err := os.WriteFile(bitmapProfilePath(a.dataDir), []byte("damaged registry"), 0600); err != nil {
		t.Fatal(err)
	}
	status := a.indexStatus()
	foundBuiltin := false
	for _, d := range status.Definitions {
		if d.ID == "bitmap-districts" {
			foundBuiltin = true
		}
		if d.ID == i.ID {
			t.Fatal("unauthenticated custom interpretation remained executable", d)
		}
	}
	if !foundBuiltin || !strings.Contains(strings.Join(status.Errors, " "), "Bitmap profiles:") {
		t.Fatal("damaged registry was silently ignored or hid builtins", status.Errors)
	}
}
