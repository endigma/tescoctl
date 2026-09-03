package tesco

import "testing"

// Facet ids captured from a live Taxonomy response. The inner encoding escapes
// spaces and "|" but leaves "&" alone, which is what the reference JS gets
// wrong.
func TestFacetRoundTrip(t *testing.T) {
	cases := []struct{ facet, name string }{
		{"b;RnJlc2glMjBGb29k", "Fresh Food"},
		{"b;Q2xvdGhpbmclMjAmJTIwQWNjZXNzb3JpZXM=", "Clothing & Accessories"},
		{"b;Q2xvdGhpbmclMjAmJTIwQWNjZXNzb3JpZXMlN0NXb21lbg==", "Clothing & Accessories|Women"},
	}

	for _, tc := range cases {
		name, err := DecodeFacet(tc.facet)
		if err != nil {
			t.Errorf("DecodeFacet(%q): %v", tc.facet, err)
			continue
		}
		if name != tc.name {
			t.Errorf("DecodeFacet(%q) = %q, want %q", tc.facet, name, tc.name)
		}
		if got := EncodeFacet(tc.name); got != tc.facet {
			t.Errorf("EncodeFacet(%q) = %q, want %q", tc.name, got, tc.facet)
		}
	}
}

func TestIsFacet(t *testing.T) {
	if !IsFacet("b;RnJlc2glMjBGb29k") {
		t.Error("a facet id should be recognised")
	}
	if IsFacet("Fresh Food") {
		t.Error("a plain category name is not a facet id")
	}
}
