package renter

import (
	"fmt"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestAddUnigueBubblePaths probes the addUniqueBubblePaths function
func TestAddUniqueBubblePaths(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a Renter
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}

	// Create some directory tree paths
	paths := []modules.SiaPath{
		modules.RootSiaPath(),
		{Path: "root"},
		{Path: "root/SubDir1"},
		{Path: "root/SubDir1/SubDir1"},
		{Path: "root/SubDir1/SubDir1/SubDir1"},
		{Path: "root/SubDir1/SubDir2"},
		{Path: "root/SubDir2"},
		{Path: "root/SubDir2/SubDir1"},
		{Path: "root/SubDir2/SubDir2"},
		{Path: "root/SubDir2/SubDir2/SubDir2"},
	}

	// Create a map of directories to be bubbled
	dirsToBubble := rt.renter.newUniqueBubblePaths()

	// Add all paths to map
	for _, path := range paths {
		err = dirsToBubble.managedAddPath(path)
		if err != nil {
			t.Fatal(err)
		}
	}

	// No randomly add more paths
	for i := 0; i < 10; i++ {
		err = dirsToBubble.managedAddPath(paths[fastrand.Intn(len(paths))])
		if err != nil {
			t.Fatal(err)
		}
	}

	// There should only be the following paths in the map
	uniquePaths := []modules.SiaPath{
		{Path: "root/SubDir1/SubDir1/SubDir1"},
		{Path: "root/SubDir1/SubDir2"},
		{Path: "root/SubDir2/SubDir1"},
		{Path: "root/SubDir2/SubDir2/SubDir2"},
	}
	if len(dirsToBubble.childDirs) != len(uniquePaths) {
		t.Fatalf("Expected %v paths in map but got %v", len(uniquePaths), len(dirsToBubble.childDirs))
	}
	for _, path := range uniquePaths {
		if _, ok := dirsToBubble.childDirs[path]; !ok {
			t.Fatal("Did not find path in map", path)
		}
	}

	// Make child directories and add a file to each
	rsc, _ := siafile.NewRSCode(1, 1)
	up := modules.FileUploadParams{
		Source:      "",
		ErasureCode: rsc,
	}
	for _, sp := range uniquePaths {
		err = rt.renter.CreateDir(sp)
		if err != nil {
			t.Fatal(err)
		}
		up.SiaPath, err = sp.Join("testFile")
		if err != nil {
			t.Fatal(err)
		}
		_, err = rt.renter.staticFileSet.NewSiaFile(up, crypto.GenerateSiaKey(crypto.RandomCipherType()), 100, 0777)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Check the metadata of the root directory. Because we added files by
	// directly calling the staticFileSet, a bubble should not have been
	// triggered and therefore the number of total files should be 0
	di, err := rt.renter.DirList(modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}
	if di[0].AggregateNumFiles != 0 {
		t.Fatal("Expected AggregateNumFiles to be 0 but got", di[0].AggregateNumFiles)
	}

	// Have uniqueBubblePaths call bubble
	dirsToBubble.managedBubbleDirs()

	// Wait for root directory to show proper number of files
	err = build.Retry(100, 100*time.Millisecond, func() error {
		di, err = rt.renter.DirList(modules.RootSiaPath())
		if err != nil {
			return err
		}
		if int(di[0].AggregateNumFiles) != len(dirsToBubble.childDirs) {
			return fmt.Errorf("Expected AggregateNumFiles to be %v but got %v", len(dirsToBubble.childDirs), di[0].AggregateNumFiles)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
