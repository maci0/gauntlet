// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211
)

func fnv64aString(s string) uint64 {
	h := uint64(fnvOffset64)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= fnvPrime64
	}
	return h
}

// Every stochastic choice the runner makes goes through these keyed draws
// rather than one shared random stream. A stream's output depends on the order
// it is consumed from, and parallel lanes consume concurrently, so a recorded
// seed would not reproduce which agent ran what, how long a retry waited, or
// what order a later loop ran in. Keyed by loop number, review name, and
// attempt instead, every choice is a pure function of the effective seed and
// inputs that are part of the journal, which is what makes a seeded --jobs > 1
// run replay like a sequential one and lets a hot-reload successor pick up the
// interrupted run's schedule as if the swap had never happened.

// mix64 is the splitmix64 finalizer. FNV alone leaves structured inputs
// correlated in low bits; this spreads them across the word so drawIndex's
// modulo is honest.
func mix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ x>>30) * 0xbf58476d1ce4e5b9
	x = (x ^ x>>27) * 0x94d049bb133111eb
	return x ^ x>>31
}

// drawIndex returns a value in [0, n) under seed, uniform because words at or
// above the largest multiple of n are rejected rather than wrapped. Rejection
// is vanishingly rare and each try mixes a fresh word, so the loop terminates.
func drawIndex(seed uint64, key string, n int) int {
	if n <= 1 {
		return 0
	}
	ceil := (^uint64(0) / uint64(n)) * uint64(n)
	keyHash := fnv64aString(key)
	keyHash ^= 0
	keyHash *= fnvPrime64

	for i := uint64(0); ; i++ {
		h := fnvAppendUint(keyHash, i)
		if v := mix64(seed ^ h); v < ceil {
			return int(v % uint64(n))
		}
	}
}

func fnvAppendUint(h uint64, val uint64) uint64 {
	if val == 0 {
		h ^= '0'
		h *= fnvPrime64
		return h
	}
	var buf [20]byte
	pos := len(buf)
	for val > 0 {
		pos--
		buf[pos] = byte('0' + (val % 10))
		val /= 10
	}
	for _, b := range buf[pos:] {
		h ^= uint64(b)
		h *= fnvPrime64
	}
	return h
}
