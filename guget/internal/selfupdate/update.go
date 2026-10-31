// Package selfupdate discovers stable Guget releases and installs verified binaries.
package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const maxArchive = 64 << 20

var ErrDevelopment = errors.New("self-update requires a release build; install a release using the instructions at https://github.com/nulifyer/guget#installation")
var releasePattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?(\+[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$`)

// Release is a validated stable release. Download locations stay private to this module.
type Release struct {
	Version   string
	Available bool
	tag       string
	assets    map[string]bool
}

type Updater struct {
	current      string
	client       *http.Client
	releaseURL   string
	downloadBase string
	goos, goarch string
}

// New accepts an optional HTTP client so callers can control network transport.
func New(current string, client *http.Client) *Updater {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Updater{current: current, client: client,
		releaseURL:   "https://api.github.com/repos/nulifyer/guget/releases/latest",
		downloadBase: "https://github.com/nulifyer/guget/releases/download/",
		goos:         runtime.GOOS, goarch: runtime.GOARCH}
}

func parseVersion(raw string) ([3]uint64, bool, bool) {
	var result [3]uint64
	match := releasePattern.FindStringSubmatch(raw)
	if match == nil {
		return result, false, false
	}
	for i := range result {
		n, err := strconv.ParseUint(match[i+1], 10, 64)
		if err != nil {
			return result, false, false
		}
		result[i] = n
	}
	return result, match[4] != "", true
}

func newer(candidate, current string) bool {
	a, preA, okA := parseVersion(candidate)
	b, preB, okB := parseVersion(current)
	if !okA || !okB || preA {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return preB
}

func (u *Updater) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "guget-self-update")
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("download exceeds size limit")
	}
	return data, nil
}

// Latest always checks GitHub, independently of the TUI notification cache.
func (u *Updater) Latest(ctx context.Context) (Release, error) {
	if _, _, ok := parseVersion(u.current); !ok {
		return Release{}, ErrDevelopment
	}
	data, err := u.get(ctx, u.releaseURL, 1<<20)
	if err != nil {
		return Release{}, fmt.Errorf("check latest release: %w", err)
	}
	var doc struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return Release{}, fmt.Errorf("read release: %w", err)
	}
	_, pre, ok := parseVersion(doc.Tag)
	if !ok || pre || doc.Draft || doc.Prerelease {
		return Release{}, errors.New("latest release is not a valid stable version")
	}
	r := Release{Version: strings.TrimPrefix(doc.Tag, "v"), Available: newer(doc.Tag, u.current), tag: doc.Tag, assets: map[string]bool{}}
	for _, a := range doc.Assets {
		r.assets[a.Name] = true
	}
	return r, nil
}

// Install updates the executable actually being run, resolving any symlink first.
// A staged file is flushed before replacement. Failed replacement leaves the old binary usable.
func (u *Updater) Install(ctx context.Context, release Release, executable string) error {
	if !release.Available || !newer(release.Version, u.current) {
		return errors.New("release is not newer than the installed version")
	}
	if (u.goos != "linux" && u.goos != "darwin" && u.goos != "windows") || (u.goarch != "amd64" && u.goarch != "arm64") {
		return fmt.Errorf("unsupported platform %s/%s", u.goos, u.goarch)
	}
	ext := ".tar.gz"
	binary := "guget"
	if u.goos == "windows" {
		ext = ".zip"
		binary += ".exe"
	}
	name := fmt.Sprintf("guget_%s_%s_%s%s", release.Version, u.goos, u.goarch, ext)
	if !release.assets[name] || !release.assets["checksums.txt"] {
		return fmt.Errorf("release is missing %s or checksums.txt", name)
	}
	target, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return fmt.Errorf("locate installed executable: %w", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("installed executable is not a regular file")
	}
	lock := target + ".update-lock"
	if err := os.Mkdir(lock, 0o700); err != nil {
		return fmt.Errorf("cannot lock %s for update; check directory permissions or an existing update lock: %w", target, err)
	}
	defer os.Remove(lock)
	base := u.downloadBase + release.tag + "/"
	checksums, err := u.get(ctx, base+"checksums.txt", 1<<20)
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	expected, err := checksumFor(checksums, name)
	if err != nil {
		return err
	}
	archive, err := u.get(ctx, base+name, maxArchive)
	if err != nil {
		return fmt.Errorf("download archive: %w", err)
	}
	actual := sha256.Sum256(archive)
	if !bytes.Equal(actual[:], expected) {
		return errors.New("archive checksum mismatch; installed binary was not changed")
	}
	data, err := extractBinary(archive, binary, ext == ".zip")
	if err != nil {
		return fmt.Errorf("extract binary: %w", err)
	}
	stage, err := os.CreateTemp(filepath.Dir(target), ".guget-update-*")
	if err != nil {
		return fmt.Errorf("stage update; check installation directory permissions: %w", err)
	}
	defer os.Remove(stage.Name())
	if _, err = stage.Write(data); err == nil {
		err = stage.Chmod(info.Mode().Perm())
	}
	if err == nil {
		err = stage.Sync()
	}
	closeErr := stage.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := replaceExecutable(stage.Name(), target); err != nil {
		return fmt.Errorf("replace executable: %w", err)
	}
	return nil
}

func checksumFor(data []byte, name string) ([]byte, error) {
	var found []byte
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[1] != name {
			continue
		}
		sum, err := hex.DecodeString(fields[0])
		if err != nil || len(sum) != sha256.Size || found != nil {
			return nil, errors.New("invalid or duplicate archive checksum")
		}
		found = sum
	}
	if found == nil {
		return nil, fmt.Errorf("checksum not found for %s", name)
	}
	return found, nil
}

func extractBinary(data []byte, name string, zipped bool) ([]byte, error) {
	var result []byte
	read := func(r io.Reader) error {
		if result != nil {
			return errors.New("archive contains duplicate binaries")
		}
		b, err := io.ReadAll(io.LimitReader(r, maxArchive+1))
		if err != nil {
			return err
		}
		if len(b) == 0 || len(b) > maxArchive {
			return errors.New("invalid binary size")
		}
		result = b
		return nil
	}
	if zipped {
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range z.File {
			if f.Name != name {
				continue
			}
			if !f.Mode().IsRegular() {
				return nil, errors.New("archive binary is not a regular file")
			}
			r, err := f.Open()
			if err != nil {
				return nil, err
			}
			err = read(r)
			closeErr := r.Close()
			if err != nil {
				return nil, err
			}
			if closeErr != nil {
				return nil, closeErr
			}
		}
	} else {
		g, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer g.Close()
		// Bound the entire expanded archive, including unrelated entries.
		tr := tar.NewReader(io.LimitReader(g, maxArchive+1))
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if h.Name != name {
				continue
			}
			if h.Typeflag != tar.TypeReg {
				return nil, errors.New("archive binary is not a regular file")
			}
			if err := read(tr); err != nil {
				return nil, err
			}
		}
	}
	if result == nil {
		return nil, errors.New("archive does not contain the executable")
	}
	return result, nil
}
