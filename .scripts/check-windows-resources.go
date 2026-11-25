//go:build ignore

// check-windows-resources verifies that Windows executables carry the icon,
// version information, and manifest that go-winres embeds during release.
//
// Usage: go run .scripts/check-windows-resources.go VERSION FILE.exe...
package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"fmt"
	"os"
	"unicode/utf16"
)

const (
	rtIcon      = 3
	rtGroupIcon = 14
	rtVersion   = 16
	rtManifest  = 24
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: go run .scripts/check-windows-resources.go VERSION FILE.exe...")
		os.Exit(2)
	}
	version := os.Args[1]
	failed := false
	for _, path := range os.Args[2:] {
		if err := check(path, version); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			failed = true
			continue
		}
		fmt.Printf("%s: icon, version %s, and manifest present\n", path, version)
	}
	if failed {
		os.Exit(1)
	}
}

func check(path, version string) error {
	file, err := pe.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	section := file.Section(".rsrc")
	if section == nil {
		return fmt.Errorf("no .rsrc section")
	}
	data, err := section.Data()
	if err != nil {
		return err
	}
	counts, err := resourceTypeCounts(data)
	if err != nil {
		return err
	}
	for _, required := range []struct {
		kind int
		name string
		min  int
	}{
		{rtGroupIcon, "icon group", 1},
		{rtIcon, "icon images", 4},
		{rtVersion, "version information", 1},
		{rtManifest, "manifest", 1},
	} {
		if counts[required.kind] < required.min {
			return fmt.Errorf("%s: found %d, want at least %d", required.name, counts[required.kind], required.min)
		}
	}
	// Version strings are UTF-16LE inside the VS_VERSIONINFO resource.
	if !bytes.Contains(data, utf16le("ProductVersion")) || !bytes.Contains(data, utf16le(version)) {
		return fmt.Errorf("version information does not contain product version %q", version)
	}
	return nil
}

// resourceTypeCounts walks the resource directory: type -> name -> language.
func resourceTypeCounts(data []byte) (map[int]int, error) {
	counts := make(map[int]int)
	types, err := directoryEntries(data, 0)
	if err != nil {
		return nil, err
	}
	for _, typeEntry := range types {
		if !typeEntry.directory {
			continue
		}
		names, err := directoryEntries(data, typeEntry.offset)
		if err != nil {
			return nil, err
		}
		counts[typeEntry.id] += len(names)
	}
	return counts, nil
}

type resourceEntry struct {
	id        int
	offset    uint32
	directory bool
}

func directoryEntries(data []byte, offset uint32) ([]resourceEntry, error) {
	if int(offset)+16 > len(data) {
		return nil, fmt.Errorf("resource directory at %d is out of range", offset)
	}
	named := binary.LittleEndian.Uint16(data[offset+12:])
	ids := binary.LittleEndian.Uint16(data[offset+14:])
	var entries []resourceEntry
	for i := 0; i < int(named)+int(ids); i++ {
		at := int(offset) + 16 + i*8
		if at+8 > len(data) {
			return nil, fmt.Errorf("resource entry at %d is out of range", at)
		}
		name := binary.LittleEndian.Uint32(data[at:])
		target := binary.LittleEndian.Uint32(data[at+4:])
		entries = append(entries, resourceEntry{
			id:        int(name & 0x7fffffff),
			offset:    target & 0x7fffffff,
			directory: target&0x80000000 != 0,
		})
	}
	return entries, nil
}

func utf16le(text string) []byte {
	var buffer bytes.Buffer
	for _, unit := range utf16.Encode([]rune(text)) {
		_ = binary.Write(&buffer, binary.LittleEndian, unit)
	}
	return buffer.Bytes()
}
