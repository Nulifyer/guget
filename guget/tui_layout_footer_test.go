package main

import "testing"

func TestPackageFooterNamesRemoveAsHelpDoes(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	m.focus = focusPackages
	for _, all := range []bool{false, true} {
		if all {
			selectAllProjectsForTest(t, m)
		}
		found := false
		for _, pair := range m.footerKeys() {
			if pair.k == "d" {
				found = true
				if pair.v != "remove" {
					t.Fatalf("all=%v: footer labels d %q; help and docs call it remove", all, pair.v)
				}
			}
		}
		if !found {
			t.Fatalf("all=%v: footer has no d key", all)
		}
	}
}
