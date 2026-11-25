// Package nugetconfig resolves package sources, credentials, and package source
// mapping the way NuGet restore does. It reads NuGet.Config files and the
// process environment but never contacts a package source.
package nugetconfig

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// DefaultSourceURL is the source NuGet writes to a newly created user config.
const DefaultSourceURL = "https://api.nuget.org/v3/index.json"

// Environment is the process state NuGet consults. The zero value uses the
// current process.
type Environment struct {
	LookupEnv func(string) (string, bool) // nil uses os.LookupEnv
	GOOS      string                      // empty uses runtime.GOOS
}

func (e Environment) lookup(name string) (string, bool) {
	if e.LookupEnv == nil {
		return os.LookupEnv(name)
	}
	return e.LookupEnv(name)
}

func (e Environment) getenv(name string) string {
	value, _ := e.lookup(name)
	return value
}

func (e Environment) goos() string {
	if e.GOOS == "" {
		return runtime.GOOS
	}
	return e.GOOS
}

// Source is an enabled package source with its resolved credentials.
// Values are environment-expanded; local feed paths are absolute.
type Source struct {
	Name     string
	URL      string
	Username string
	Password string
}

// IsHTTP reports whether the source is an HTTP(S) feed rather than a local or
// UNC directory.
func (s Source) IsHTTP() bool {
	return isHTTP(s.URL)
}

// File is a configuration file consumed while resolving settings.
type File struct {
	Path   string
	Digest [sha256.Size]byte
}

// Settings is the effective NuGet configuration for one restore root.
type Settings struct {
	Files    []File   // consumed files, highest priority first
	Sources  []Source // enabled sources in restore order
	Mapping  *Mapping // nil when package source mapping is not configured
	Warnings []string // non-fatal problems; never contain secret values
}

// Load resolves the configuration NuGet restore uses for a project whose
// directory is root: the NuGet.Config chain from root to the filesystem root,
// then user, additional user, and machine-wide configuration. Like NuGet, any
// unreadable or invalid file makes the whole configuration unusable. On error
// the returned Settings is non-nil and reports only the consumed Files, so
// callers can tell a stable invalid file from one that changed while read.
func Load(root string, env Environment) (*Settings, error) {
	return LoadCached(root, env, nil)
}

// Cache shares file system reads between the Load calls of one resolution,
// such as one per project directory of a workspace, whose configuration
// chains share ancestor, user, and machine files. Each file is inspected,
// read, and parsed once; every Load that shares the cache consumes that one
// revision and still reports it in Settings.Files. Use a new Cache for each
// resolution so later calls see later changes. A Cache is not safe for
// concurrent use.
type Cache struct {
	stats map[string]statResult
	files map[string]readResult
	dirs  map[string]dirResult
}

type dirResult struct {
	entries []os.DirEntry
	err     error
}

type statResult struct {
	info os.FileInfo
	err  error
}

type readResult struct {
	read   bool // the bytes were read, so their digest was consumed
	digest [sha256.Size]byte
	file   *configFile
	err    error
}

// NewCache returns an empty Cache.
func NewCache() *Cache {
	return &Cache{stats: make(map[string]statResult), files: make(map[string]readResult), dirs: make(map[string]dirResult)}
}

func (c *Cache) stat(path string) (os.FileInfo, error) {
	if c == nil {
		return os.Stat(path)
	}
	if cached, ok := c.stats[path]; ok {
		return cached.info, cached.err
	}
	info, err := os.Stat(path)
	c.stats[path] = statResult{info: info, err: err}
	return info, err
}

// read returns the digest and parsed form of path. A parse error keeps the
// digest of the bytes read.
func (c *Cache) read(path string) readResult {
	if c != nil {
		if cached, ok := c.files[path]; ok {
			return cached
		}
	}
	var result readResult
	data, err := os.ReadFile(path)
	if err != nil {
		result.err = fmt.Errorf("read NuGet configuration %s: %w", path, err)
	} else {
		result.read, result.digest = true, sha256.Sum256(data)
		if result.file, err = parseConfigFile(path, data); err != nil {
			result.err = fmt.Errorf("invalid NuGet configuration %s: %w", path, err)
		}
	}
	if c != nil {
		c.files[path] = result
	}
	return result
}

func (c *Cache) readDir(dir string) ([]os.DirEntry, error) {
	if c == nil {
		return os.ReadDir(dir)
	}
	if cached, ok := c.dirs[dir]; ok {
		return cached.entries, cached.err
	}
	entries, err := os.ReadDir(dir)
	c.dirs[dir] = dirResult{entries: entries, err: err}
	return entries, err
}

// LoadCached is Load with reads shared through cache. A nil cache reads the
// file system on every call, as Load does.
func LoadCached(root string, env Environment, cache *Cache) (*Settings, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return &Settings{}, fmt.Errorf("resolve configuration root %s: %w", root, err)
	}
	loader := fileLoader{cache: cache}
	var files []*configFile
	var errs []error
	add := func(path string) bool {
		file, found, err := loader.load(path)
		if err != nil {
			errs = append(errs, err)
		}
		if file != nil {
			files = append(files, file)
		}
		return found || err != nil
	}

	for _, dir := range ancestorDirs(root) {
		for _, name := range configFileNames {
			// NuGet uses the first existing name in each directory.
			if add(filepath.Join(dir, name)) {
				break
			}
		}
	}
	if userDir := userSettingsDir(env); userDir != "" {
		userConfig := filepath.Join(userDir, "NuGet.Config")
		if !add(userConfig) {
			// NuGet creates a missing user config containing nuget.org.
			files = append(files, defaultUserConfig(userConfig))
		}
		for _, path := range configsInDir(cache, filepath.Join(userDir, "config"), "NuGet.Config", env) {
			add(path)
		}
	}
	userFiles := len(files)
	machineDir := machineSettingsDir(env)
	for _, path := range configsInDir(cache, filepath.Join(machineDir, "Config"), "", env) {
		add(path)
	}
	var defaults *configFile
	if file, _, err := loader.load(filepath.Join(machineDir, "NuGetDefaults.config")); err != nil {
		errs = append(errs, err)
	} else if file != nil {
		defaults = file
	}
	if len(errs) > 0 {
		return &Settings{Files: loader.consumed}, errors.Join(errs...)
	}
	settings := resolve(files, userFiles, defaults, env)
	settings.Files = loader.consumed
	return settings, nil
}

// CandidatePaths lists the configuration files whose creation, removal, or
// change can alter Load(root). Files found in configuration directories are
// reported through Settings.Files instead.
func CandidatePaths(root string, env Environment) []string {
	var paths []string
	if absolute, err := filepath.Abs(root); err == nil {
		for _, dir := range ancestorDirs(absolute) {
			for _, name := range configFileNames {
				paths = append(paths, filepath.Join(dir, name))
			}
		}
	}
	if userDir := userSettingsDir(env); userDir != "" {
		paths = append(paths, filepath.Join(userDir, "NuGet.Config"))
	}
	return append(paths, filepath.Join(machineSettingsDir(env), "NuGetDefaults.config"))
}

// NuGet probes these names in order on case-sensitive filesystems. A
// case-insensitive filesystem matches the first probe for any spelling.
var configFileNames = []string{"nuget.config", "NuGet.config", "NuGet.Config"}

func ancestorDirs(root string) []string {
	var dirs []string
	for dir := root; ; dir = filepath.Dir(dir) {
		dirs = append(dirs, dir)
		if filepath.Dir(dir) == dir {
			return dirs
		}
	}
}

func userSettingsDir(env Environment) string {
	if env.goos() == "windows" {
		if appData := env.getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "NuGet")
		}
		return ""
	}
	// NuGet uses DOTNET_CLI_HOME whenever it is set. An empty value makes the
	// user directory relative to the current directory, as restore does.
	home, set := env.lookup("DOTNET_CLI_HOME")
	if !set {
		if home = env.getenv("HOME"); home == "" {
			return ""
		}
	}
	dir, err := filepath.Abs(filepath.Join(home, ".nuget", "NuGet"))
	if err != nil {
		return ""
	}
	return dir
}

func machineSettingsDir(env Environment) string {
	switch env.goos() {
	case "windows":
		base := env.getenv("ProgramFiles(x86)")
		if base == "" {
			base = env.getenv("ProgramFiles")
		}
		return filepath.Join(base, "NuGet")
	case "darwin":
		return "/Library/Application Support/NuGet"
	default:
		if base := env.getenv("NUGET_COMMON_APPLICATION_DATA"); base != "" {
			return filepath.Join(base, "NuGet")
		}
		return "/etc/opt/NuGet"
	}
}

// configsInDir lists the configuration files directly inside dir in priority
// order. Linux matches *.config and *.Config and the excluded name exactly;
// case-insensitive platforms compare names without case.
func configsInDir(cache *Cache, dir, exclude string, env Environment) []string {
	entries, err := cache.readDir(dir)
	if err != nil {
		return nil
	}
	caseSensitive := env.goos() == "linux"
	same := func(a, b string) bool { return a == b || !caseSensitive && strings.EqualFold(a, b) }
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		ext := filepath.Ext(name)
		if entry.IsDir() || !(same(ext, ".config") || same(ext, ".Config")) || (exclude != "" && same(name, exclude)) {
			continue
		}
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if caseSensitive {
			return names[i] < names[j]
		}
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	paths := make([]string, len(names))
	for i, name := range names {
		paths[i] = filepath.Join(dir, name)
	}
	return paths
}

func defaultUserConfig(path string) *configFile {
	return &configFile{
		path:    path,
		sources: section[sourceItem]{items: []sourceItem{{key: "nuget.org", value: DefaultSourceURL}}},
	}
}

// fileLoader reads each file identity once, so aliases on case-insensitive
// filesystems and symlinked configs are consumed and diagnosed once.
type fileLoader struct {
	cache    *Cache
	seen     []os.FileInfo
	consumed []File
}

// load reports found for an existing path even when its identity was already
// consumed; the file is nil then.
func (l *fileLoader) load(path string) (*configFile, bool, error) {
	info, err := l.cache.stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect NuGet configuration %s: %w", path, err)
	}
	for _, previous := range l.seen {
		if os.SameFile(previous, info) {
			return nil, true, nil
		}
	}
	l.seen = append(l.seen, info)
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("NuGet configuration is not a regular file: %s", path)
	}
	read := l.cache.read(path)
	if !read.read {
		return nil, false, read.err
	}
	l.consumed = append(l.consumed, File{Path: path, Digest: read.digest})
	if read.err != nil {
		return nil, false, read.err
	}
	return read.file, true, nil
}

func isHTTP(value string) bool {
	lower := strings.ToLower(value)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}
