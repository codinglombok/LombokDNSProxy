// compile_lists — LombokDNSProxy
// Reads all .txt blocklist files from -input dir, merges them,
// and writes a compact binary representation to -output path.
// Used by: task lists:compile
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func main() {
	inputDir := flag.String("input", "lists/", "Directory containing .txt blocklist files")
	outputFile := flag.String("output", "lists/blocklist.bin", "Output binary trie file")
	flag.Parse()

	start := time.Now()
	fmt.Printf("▶ Compiling blocklists from %s\n", *inputDir)

	domains := make(map[string]struct{})

	// Walk input directory, load all .txt files
	entries, err := os.ReadDir(*inputDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ Cannot read dir %s: %v\n", *inputDir, err)
		os.Exit(1)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		path := filepath.Join(*inputDir, e.Name())
		n, err := loadFile(path, domains)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ Skip %s: %v\n", e.Name(), err)
			continue
		}
		fmt.Printf("  ✓ %-40s %8d domains\n", e.Name(), n)
	}

	// Sort and deduplicate
	sorted := make([]string, 0, len(domains))
	for d := range domains {
		sorted = append(sorted, d)
	}
	sort.Strings(sorted)

	// Write binary format:
	// Header: magic(4) + version(1) + count(4)
	// Body:   for each domain: len(1) + bytes
	out, err := os.Create(*outputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ Cannot create output: %v\n", err)
		os.Exit(1)
	}
	defer out.Close()

	w := bufio.NewWriterSize(out, 4*1024*1024) // 4MB buffer

	// Magic: "LDBK" + version byte 0x01
	w.WriteString("LDBK")
	w.WriteByte(0x01)

	// Count (uint32 big-endian)
	var countBuf [4]byte
	binary.BigEndian.PutUint32(countBuf[:], uint32(len(sorted)))
	w.Write(countBuf[:])

	// Domains
	for _, d := range sorted {
		if len(d) > 253 {
			continue // DNS max label length
		}
		w.WriteByte(byte(len(d)))
		w.WriteString(d)
	}

	if err := w.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "✗ Write error: %v\n", err)
		os.Exit(1)
	}

	stat, _ := out.Stat()
	fmt.Printf("\n✓ Compiled %d domains → %s (%.1f MB) in %s\n",
		len(sorted), *outputFile,
		float64(stat.Size())/1024/1024,
		time.Since(start).Round(time.Millisecond),
	)
}

// loadFile reads one hosts/bare-domain file and adds entries to domains map.
// Returns number of new unique domains added.
func loadFile(path string, domains map[string]struct{}) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	before := len(domains)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		domain := parseDomain(line)
		if domain == "" || strings.Contains(domain, " ") {
			continue
		}
		domains[strings.ToLower(domain)] = struct{}{}
	}
	return len(domains) - before, scanner.Err()
}

func parseDomain(line string) string {
	if idx := strings.IndexByte(line, '#'); idx != -1 {
		line = strings.TrimSpace(line[:idx])
	}
	fields := strings.Fields(line)
	switch len(fields) {
	case 0:
		return ""
	case 1:
		return fields[0]
	default:
		if fields[0] == "0.0.0.0" || fields[0] == "127.0.0.1" || fields[0] == "::" {
			return fields[1]
		}
		return fields[0]
	}
}
