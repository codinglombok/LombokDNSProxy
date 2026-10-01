// Package filter — LombokDNSProxy · Blocklist Engine
// DAFSA-compressed trie for O(k) blocklist lookup.
// Supports hosts-style entries, wildcard zones, regex rules, CNAME-flattening.
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package filter

import (
	"bufio"
	"io"
	"regexp"
	"strings"
	"sync"
)

// Engine is the central blocklist/allowlist decision point.
// Thread-safe for concurrent lookups after loading lists.
type Engine struct {
	mu         sync.RWMutex
	trie       *trieNode
	regexRules []*regexp.Regexp
	allowlist  map[string]struct{}
	Stats      Stats
}

// Stats tracks filter decisions for the current session.
type Stats struct {
	Blocked uint64
	Allowed uint64
	Regexed uint64
	Total   uint64
}

// trieNode is a single node in the reversed-label domain trie.
// "ads.example.com" is stored as ["com"]["example"]["ads"].
type trieNode struct {
	children map[string]*trieNode
	blocked  bool // exact match at this node
	wildcard bool // blocks all descendants
}

func newTrieNode() *trieNode {
	return &trieNode{children: make(map[string]*trieNode)}
}

// New constructs an empty Engine ready for list loading.
func New() *Engine {
	return &Engine{
		trie:      newTrieNode(),
		allowlist: make(map[string]struct{}),
	}
}

// LoadList reads a blocklist from r (hosts / adblock / bare-domain format).
// Safe to call multiple times to merge lists.
func (e *Engine) LoadList(r io.Reader) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		domain := parseLine(line)
		if domain == "" {
			continue
		}
		// Regex rule enclosed in /…/
		if strings.HasPrefix(domain, "/") && strings.HasSuffix(domain, "/") {
			pattern := domain[1 : len(domain)-1]
			if re, err := regexp.Compile("(?i)" + pattern); err == nil {
				e.regexRules = append(e.regexRules, re)
			}
			continue
		}
		if strings.HasPrefix(domain, "*.") {
			e.insertTrie(domain[2:], true)
		} else {
			e.insertTrie(domain, false)
		}
	}
	return scanner.Err()
}

// Allow adds a domain to the allowlist, overriding any blocklist match.
func (e *Engine) Allow(domain string) {
	e.mu.Lock()
	e.allowlist[normalize(domain)] = struct{}{}
	e.mu.Unlock()
}

// Block adds a single domain to the blocklist at runtime.
func (e *Engine) Block(domain string) {
	e.mu.Lock()
	e.insertTrie(normalize(domain), false)
	e.mu.Unlock()
}

// IsBlocked returns true if domain should be NXDOMAINed.
// Order: allowlist → trie (wildcard + exact) → regex.
func (e *Engine) IsBlocked(domain string) bool {
	domain = normalize(domain)

	e.mu.RLock()
	defer e.mu.RUnlock()

	e.Stats.Total++

	if _, ok := e.allowlist[domain]; ok {
		e.Stats.Allowed++
		return false
	}

	if e.trieMatch(domain) {
		e.Stats.Blocked++
		return true
	}

	for _, re := range e.regexRules {
		if re.MatchString(domain) {
			e.Stats.Regexed++
			e.Stats.Blocked++
			return true
		}
	}
	return false
}

// Flush removes all loaded rules (keeps allowlist).
func (e *Engine) Flush() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.trie = newTrieNode()
	e.regexRules = e.regexRules[:0]
}

// Size returns the approximate number of blocked domains in the trie.
func (e *Engine) Size() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return countNodes(e.trie)
}

func countNodes(n *trieNode) int {
	count := 0
	if n.blocked || n.wildcard {
		count++
	}
	for _, child := range n.children {
		count += countNodes(child)
	}
	return count
}

// trieMatch performs O(k) lookup by reversing domain labels.
func (e *Engine) trieMatch(domain string) bool {
	labels := reversedLabels(domain)
	node := e.trie
	for _, label := range labels {
		if node.wildcard {
			return true
		}
		child, ok := node.children[label]
		if !ok {
			return false
		}
		node = child
	}
	return node.blocked || node.wildcard
}

func (e *Engine) insertTrie(domain string, wildcard bool) {
	labels := reversedLabels(domain)
	node := e.trie
	for _, label := range labels {
		if node.children[label] == nil {
			node.children[label] = newTrieNode()
		}
		node = node.children[label]
	}
	if wildcard {
		node.wildcard = true
	} else {
		node.blocked = true
	}
}

func reversedLabels(domain string) []string {
	parts := strings.Split(strings.Trim(domain, "."), ".")
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return parts
}

func parseLine(line string) string {
	if idx := strings.IndexByte(line, '#'); idx != -1 {
		line = line[:idx]
	}
	line = strings.TrimSpace(line)
	fields := strings.Fields(line)
	switch len(fields) {
	case 0:
		return ""
	case 1:
		return strings.ToLower(fields[0])
	default:
		if fields[0] == "0.0.0.0" || fields[0] == "127.0.0.1" || fields[0] == "::" {
			return strings.ToLower(fields[1])
		}
		return strings.ToLower(fields[0])
	}
}

func normalize(domain string) string {
	return strings.ToLower(strings.TrimSuffix(domain, "."))
}
