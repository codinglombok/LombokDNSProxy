// Package cache — LombokDNSProxy · ECC-verified DNS Cache
// LRU+TTL cache with Reed-Solomon RS(255,239) integrity via LombokECC.
// Sharded into N independent LRU shards to reduce mutex contention.
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package cache

import (
	"container/list"
	"hash/fnv"
	"sync"
	"time"

	"github.com/miekg/dns"
)

const defaultShards = 16

// Key uniquely identifies a DNS cache entry.
type Key struct {
	Domain string
	Qtype  uint16
}

// Record is the decoded form returned to callers.
type Record struct {
	Msg    *dns.Msg
	Expiry time.Time
}

// ToResponse recreates a dns.Msg with the correct ID and adjusted TTLs.
func (r *Record) ToResponse(id uint16) *dns.Msg {
	m := r.Msg.Copy()
	m.Id = id
	remaining := time.Until(r.Expiry)
	if remaining < 0 {
		remaining = 1 * time.Second
	}
	ttl := uint32(remaining.Seconds())
	for _, rr := range m.Answer {
		rr.Header().Ttl = ttl
	}
	return m
}

// entry is the internal cache value.
type entry struct {
	wire   []byte        // wireformat DNS response
	parity []byte        // RS(255,239) ECC parity
	expiry time.Time
	elem   *list.Element // pointer in per-shard LRU list
}

// shard is one independent LRU+TTL cache shard with its own mutex.
type shard struct {
	mu       sync.Mutex
	capacity int
	entries  map[Key]*entry
	lru      *list.List
}

// ECCProvider is a minimal interface to LombokECC.
type ECCProvider interface {
	Encode(data []byte) (parity []byte, err error)
	Verify(data, parity []byte) (corrected []byte, err error)
}

// Cache is a sharded, ECC-verified DNS cache.
type Cache struct {
	shards []*shard
	nshards int
	ecc    ECCProvider
	Stats  CacheStats
	mu     sync.Mutex // guards Stats only
}

// CacheStats holds aggregate hit/miss counters.
type CacheStats struct {
	Hits         uint64
	Misses       uint64
	Evictions    uint64
	ECCCorrections uint64
	ECCFailures  uint64
}

// New creates a sharded cache with total capacity spread across nShards.
// If nShards <= 0, defaultShards (16) is used.
func New(totalCapacity int, ecc ECCProvider, nShards int) *Cache {
	if nShards <= 0 {
		nShards = defaultShards
	}
	perShard := totalCapacity / nShards
	if perShard < 1 {
		perShard = 1
	}
	c := &Cache{
		shards:  make([]*shard, nShards),
		nshards: nShards,
		ecc:     ecc,
	}
	for i := range c.shards {
		c.shards[i] = &shard{
			capacity: perShard,
			entries:  make(map[Key]*entry, perShard),
			lru:      list.New(),
		}
	}
	return c
}

// Get retrieves a valid, non-expired, ECC-verified cache record.
func (c *Cache) Get(domain string, qtype uint16) (*Record, bool) {
	k := Key{Domain: domain, Qtype: qtype}
	s := c.shardFor(k)

	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.entries[k]
	if !ok {
		c.incStat(&c.Stats.Misses)
		return nil, false
	}
	if time.Now().After(e.expiry) {
		s.evict(k, e)
		c.incStat(&c.Stats.Misses)
		return nil, false
	}

	// ECC verification
	data, err := c.ecc.Verify(e.wire, e.parity)
	if err != nil {
		s.evict(k, e)
		c.incStat(&c.Stats.ECCFailures)
		c.incStat(&c.Stats.Misses)
		return nil, false
	}
	if len(data) != len(e.wire) {
		c.incStat(&c.Stats.ECCCorrections)
		e.wire = data
	}

	s.lru.MoveToFront(e.elem)
	c.incStat(&c.Stats.Hits)

	m := new(dns.Msg)
	if err := m.Unpack(data); err != nil {
		s.evict(k, e)
		return nil, false
	}
	return &Record{Msg: m, Expiry: e.expiry}, true
}

// Set stores a DNS response with ECC parity. TTL is derived from answer RRs.
func (c *Cache) Set(domain string, qtype uint16, msg *dns.Msg) {
	if msg == nil || len(msg.Answer) == 0 {
		return
	}
	ttl := minTTL(msg)
	if ttl == 0 {
		return
	}

	wire, err := msg.Pack()
	if err != nil {
		return
	}
	parity, _ := c.ecc.Encode(wire) // best-effort; nil parity is handled on read

	k := Key{Domain: domain, Qtype: qtype}
	s := c.shardFor(k)

	s.mu.Lock()
	defer s.mu.Unlock()

	expiry := time.Now().Add(time.Duration(ttl) * time.Second)

	if existing, ok := s.entries[k]; ok {
		existing.wire = wire
		existing.parity = parity
		existing.expiry = expiry
		s.lru.MoveToFront(existing.elem)
		return
	}

	if s.lru.Len() >= s.capacity {
		s.evictLRU()
		c.incStat(&c.Stats.Evictions)
	}

	elem := s.lru.PushFront(k)
	s.entries[k] = &entry{wire: wire, parity: parity, expiry: expiry, elem: elem}
}

// Flush removes all entries from every shard.
func (c *Cache) Flush() {
	for _, s := range c.shards {
		s.mu.Lock()
		s.entries = make(map[Key]*entry, s.capacity)
		s.lru.Init()
		s.mu.Unlock()
	}
}

// Size returns the total number of cached entries.
func (c *Cache) Size() int {
	total := 0
	for _, s := range c.shards {
		s.mu.Lock()
		total += len(s.entries)
		s.mu.Unlock()
	}
	return total
}

// shardFor selects the shard responsible for key k using FNV hash.
func (c *Cache) shardFor(k Key) *shard {
	h := fnv.New32a()
	h.Write([]byte(k.Domain))
	h.Write([]byte{byte(k.Qtype >> 8), byte(k.Qtype)})
	return c.shards[h.Sum32()%uint32(c.nshards)]
}

func (s *shard) evict(k Key, e *entry) {
	s.lru.Remove(e.elem)
	delete(s.entries, k)
}

func (s *shard) evictLRU() {
	back := s.lru.Back()
	if back == nil {
		return
	}
	k := back.Value.(Key)
	if e, ok := s.entries[k]; ok {
		s.evict(k, e)
	}
}

func (c *Cache) incStat(counter *uint64) {
	c.mu.Lock()
	*counter++
	c.mu.Unlock()
}

func minTTL(msg *dns.Msg) uint32 {
	const cap = uint32(3600)
	min := cap
	for _, rr := range msg.Answer {
		if t := rr.Header().Ttl; t < min {
			min = t
		}
	}
	return min
}
