package scaffold

import (
	"bytes"
	"io/fs"
	"strings"
	"testing"

	"github.com/gmreyer/lorekeep/internal/world"
)

// The example world is written the way the editor saves, so a writer's first
// save of an example file changes only what they edited.
func TestExampleWorldInSavedForm(t *testing.T) {
	err := fs.WalkDir(files, "files/example/world", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		src, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		// Adding a key and removing it again re-encodes the frontmatter twice;
		// a file in saved form comes back byte for byte.
		set, _, err := world.EditFrontmatter(src, []world.Change{{Path: []string{"zz_probe"}, Value: 1}})
		if err != nil {
			t.Errorf("%s: %v", p, err)
			return nil
		}
		back, _, err := world.EditFrontmatter(set, []world.Change{{Path: []string{"zz_probe"}, Delete: true}})
		if err != nil {
			t.Errorf("%s: %v", p, err)
			return nil
		}
		if !bytes.Equal(back, src) {
			t.Errorf("%s is not in saved form; a first save would rewrite it as:\n%s", p, back)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
