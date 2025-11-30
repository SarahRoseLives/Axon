package files

import (
    "hash/fnv"
    "strings"
)

// Standard configuration for our mesh
const (
    FilterSize = 64 * 1024 * 8 // 64KB in bits = 524,288 bits
    HashCount  = 5             // Optimal for ~40k items in 64KB
)

type BloomFilter struct {
    BitSet []byte `json:"bitset"`
}

func NewBloomFilter() *BloomFilter {
    return &BloomFilter{
        BitSet: make([]byte, FilterSize/8),
    }
}

// Add inserts a string (and its keywords) into the filter
func (bf *BloomFilter) Add(s string) {
    tokens := tokenize(s)
    for _, t := range tokens {
        bf.addTerm(t)
    }
}

// Test checks if a string (or keyword) is likely in the set
func (bf *BloomFilter) Test(s string) bool {
    s = strings.ToLower(strings.TrimSpace(s))
    // We assume the user searches for a keyword, e.g., "matrix"
    // If the filter contains "matrix", it returns true.
    return bf.testTerm(s)
}

// Internal add of a specific term
func (bf *BloomFilter) addTerm(term string) {
    h1, h2 := hash(term)
    for i := 0; i < HashCount; i++ {
        idx := getIndex(h1, h2, i)
        byteIdx := idx / 8
        bitIdx := idx % 8
        bf.BitSet[byteIdx] |= (1 << bitIdx)
    }
}

func (bf *BloomFilter) testTerm(term string) bool {
    h1, h2 := hash(term)
    for i := 0; i < HashCount; i++ {
        idx := getIndex(h1, h2, i)
        byteIdx := idx / 8
        bitIdx := idx % 8
        if (bf.BitSet[byteIdx] & (1 << bitIdx)) == 0 {
            return false
        }
    }
    return true
}

// tokenize splits a filename into searchable keywords
// e.g., "The.Matrix.1999.mkv" -> ["the", "matrix", "1999", "mkv"]
func tokenize(s string) []string {
    s = strings.ToLower(s)
    f := func(c rune) bool {
        return !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'))
    }
    return strings.FieldsFunc(s, f)
}

// Double Hashing (Kirsch-Mitzenmacher optimization)
// hash_i = (h1 + i*h2) % m
func getIndex(h1, h2 uint64, i int) uint64 {
    return (h1 + uint64(i)*h2) % uint64(FilterSize)
}

func hash(s string) (uint64, uint64) {
    h := fnv.New64a()
    h.Write([]byte(s))
    h1 := h.Sum64()

    h.Reset()
    h.Write([]byte(s))
    h.Write([]byte{1}) // Slight variation for h2
    h2 := h.Sum64()
    return h1, h2
}