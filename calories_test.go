package fitactivity

import (
	"path/filepath"
	"testing"

	"github.com/wisborg/fitactivity/fittest"
)

// A FIT record's own calories and resistance are read; a file that
// records neither has neither, not a zero of each.
func TestDecodeCaloriesAndResistance(t *testing.T) {
	opts := fittest.DefaultOptions()
	opts.Count = 120
	opts.CaloriesPerSecond, opts.Resistance = 0.5, 12
	path := filepath.Join(t.TempDir(), "machine.fit")
	if err := fittest.WriteFile(path, opts); err != nil {
		t.Fatal(err)
	}
	tr, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	last := tr.Samples[len(tr.Samples)-1]
	if !last.HasCalories || last.Calories != 59 || !last.HasResistance || last.Resistance != 12 {
		t.Errorf("the last record reads calories %v (%v), resistance %v (%v); want 59 and 12",
			last.Calories, last.HasCalories, last.Resistance, last.HasResistance)
	}

	opts = fittest.DefaultOptions()
	opts.Count = 10
	plain := filepath.Join(t.TempDir(), "plain.fit")
	if err := fittest.WriteFile(plain, opts); err != nil {
		t.Fatal(err)
	}
	if tr, err = Read(plain); err != nil {
		t.Fatal(err)
	}
	if s := tr.Samples[0]; s.HasCalories || s.HasResistance {
		t.Errorf("a file that writes neither reads %+v", s)
	}
}
