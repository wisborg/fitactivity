package strava

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// CacheTTL is how long an answer from Strava is kept: a day. Strava's API
// Policy allows seven, but also wants a deletion on Strava to show within
// 48 hours, and an answer read again after that could show one that should
// not; a day keeps well inside both. Older is not just unread but deleted.
const CacheTTL = 24 * time.Hour

// Cache keeps Strava's answers for an activity, so the commands run on it
// one after another in a session ask Strava once.
type Cache struct {
	dir string
	now func() time.Time
}

// DefaultCacheDir is where the cache is kept unless a program says
// otherwise: under the system's cache directory, which it may empty.
func DefaultCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "fitactivity", "strava"), nil
}

// OpenCache opens the cache in dir, deleting whatever in it is older than
// CacheTTL.
func OpenCache(dir string) (*Cache, error) {
	c := &Cache{dir: dir, now: time.Now}
	return c, c.Expire()
}

// Dir is where the cache is.
func (c *Cache) Dir() string { return c.dir }

// Expire deletes every answer older than CacheTTL.
func (c *Cache) Expire() error {
	if c == nil {
		return nil
	}
	return filepath.WalkDir(c.dir, func(path string, d os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if c.now().Sub(info.ModTime()) > CacheTTL {
			return os.Remove(path)
		}
		return nil
	})
}

// Entries is how many answers the cache holds for athlete, and when the
// oldest of them was kept.
func (c *Cache) Entries(athlete int64) (int, time.Time) {
	if c == nil {
		return 0, time.Time{}
	}
	files, _ := os.ReadDir(c.athleteDir(athlete))
	n, oldest := 0, time.Time{}
	for _, f := range files {
		info, err := f.Info()
		if err != nil || f.IsDir() {
			continue
		}
		n++
		if oldest.IsZero() || info.ModTime().Before(oldest) {
			oldest = info.ModTime()
		}
	}
	return n, oldest
}

// Forget deletes everything kept for athlete.
func (c *Cache) Forget(athlete int64) error {
	if c == nil {
		return nil
	}
	return os.RemoveAll(c.athleteDir(athlete))
}

func (c *Cache) athleteDir(athlete int64) string {
	return filepath.Join(c.dir, strconv.FormatInt(athlete, 10))
}

func (c *Cache) path(athlete, id int64, kind string) string {
	return filepath.Join(c.athleteDir(athlete), fmt.Sprintf("%d.%s.json", id, kind))
}

// get is the answer of kind for activity id, when one younger than
// CacheTTL is kept.
func (c *Cache) get(athlete, id int64, kind string) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	p := c.path(athlete, id, kind)
	info, err := os.Stat(p)
	if err != nil || c.now().Sub(info.ModTime()) > CacheTTL {
		return nil, false
	}
	data, err := os.ReadFile(p)
	return data, err == nil
}

// put keeps the answer of kind for activity id, for its owner alone.
func (c *Cache) put(athlete, id int64, kind string, data []byte) error {
	if c == nil {
		return nil
	}
	dir := c.athleteDir(athlete)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".answer-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), c.path(athlete, id, kind))
}

// Refresh deletes what is kept for activity id, so the next fetch asks
// Strava again.
func (c *Cache) Refresh(athlete, id int64) error {
	if c == nil {
		return nil
	}
	files, _ := os.ReadDir(c.athleteDir(athlete))
	prefix := strconv.FormatInt(id, 10) + "."
	for _, f := range files {
		if strings.HasPrefix(f.Name(), prefix) {
			if err := os.Remove(filepath.Join(c.athleteDir(athlete), f.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
