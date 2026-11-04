// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package sbom

import (
	"slices"
	"strings"
	"testing"
)

// FuzzSPDXID drives the license-grant classifier with arbitrary text, as it
// runs over license files in the module cache (untrusted in the sense that a
// module can ship any text). The result lands in sbom.json as
// licenses[].license.id, so a wrong identifier is a wrong claim about terms
// a release ships under. The harness pins: determinism, that the result is
// one of the closed SPDX set or empty (never a nearest guess), and that a
// non-empty result only occurs when the body's markers for that license are
// actually present — so a body with no license text can never be named MIT.
func FuzzSPDXID(f *testing.F) {
	seeds := []string{
		"MIT License\n\nPermission is hereby granted, free of charge, to any person",
		"Apache License\nVersion 2.0, January 2004",
		"Redistribution and use in source and binary forms.\nNeither the names of its contributors may be used",
		"Redistribution and use in source and binary forms, with or without modification",
		"Permission to use, copy, modify, and/or distribute this software for any purpose",
		"Permission to use, copy, modify, and/or distribute this software for any purpose\nprovided that the above copyright notice",
		"Permission is hereby granted without restriction",
		"",
		"Some unrelated copyright text",
		"MIT License " + "MIT License ",
		"\x00\xff\xfe Apache License Version 2.0",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, body string) {
		got := spdxID(body)
		if again := spdxID(body); again != got {
			t.Fatalf("spdxID(%q) is not deterministic: %q vs %q", body, got, again)
		}
		if got == "" {
			return
		}
		allowed := []string{"MIT", "Apache-2.0", "BSD-2-Clause", "BSD-3-Clause", "ISC", "0BSD"}
		if !slices.Contains(allowed, got) {
			t.Fatalf("spdxID(%q) = %q, not in {%s}", body, got, strings.Join(allowed, ", "))
		}
		// Marker requirement: each id needs its identifying clauses in body.
		switch got {
		case "Apache-2.0":
			if !strings.Contains(body, "Apache License") || !strings.Contains(body, "Version 2.0") {
				t.Fatalf("spdxID(%q) = Apache-2.0 without its markers", body)
			}
		case "MIT":
			if !strings.Contains(body, "MIT License") &&
				!(strings.Contains(body, "Permission is hereby granted") && strings.Contains(body, "without restriction")) {
				t.Fatalf("spdxID(%q) = MIT without its markers", body)
			}
		case "BSD-2-Clause", "BSD-3-Clause":
			if !strings.Contains(body, "Redistribution and use in source and binary forms") {
				t.Fatalf("spdxID(%q) = %s without its markers", body, got)
			}
			if got == "BSD-3-Clause" && !nonEndorsement.MatchString(body) {
				t.Fatalf("spdxID(%q) = BSD-3-Clause without an endorsement clause", body)
			}
		case "ISC", "0BSD":
			if !strings.Contains(body, "Permission to use, copy, modify, and/or distribute this software for any purpose") {
				t.Fatalf("spdxID(%q) = %s without its markers", body, got)
			}
			if got == "ISC" && !strings.Contains(body, "provided that the above copyright notice") {
				t.Fatalf("spdxID(%q) = ISC without its retention clause", body)
			}
			if got == "0BSD" && strings.Contains(body, "provided that the above copyright notice") {
				t.Fatalf("spdxID(%q) = 0BSD but body carries the ISC retention clause", body)
			}
		}
	})
}
