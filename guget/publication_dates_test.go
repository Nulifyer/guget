package main

import (
	"strings"
	"testing"
	"time"
)

func TestPublicationAgeRejectsUnknownAndFutureDates(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		date time.Time
		want string
	}{
		{time.Time{}, ""},
		{time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC), ""},
		{now.Add(time.Second), ""},
		{now.Add(-time.Hour), "today"},
		{now.Add(-24 * time.Hour), "1 day ago"},
		{now.Add(-60 * 24 * time.Hour), "2 months ago"},
		{now.Add(-365 * 24 * time.Hour), "1 year ago"},
	}
	for _, tc := range cases {
		if got := timeAgoAt(tc.date, now); got != tc.want {
			t.Errorf("age(%s)=%q, want %q", tc.date, got, tc.want)
		}
	}
}

func TestPublicPublicationDatePreservesFeedProvenance(t *testing.T) {
	feed := time.Now().Add(-time.Hour)
	public := time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)
	info := &PackageInfo{ID: "Example", Versions: []PackageVersion{{SemVer: ParseSemVer("1.0.0"), Published: feed}}}
	upstream := &PackageInfo{ID: "Example", Versions: []PackageVersion{{SemVer: ParseSemVer("1.0.0"), Published: public}, {SemVer: ParseSemVer("2.0.0"), Published: public}}}
	enrichFromNugetOrg(info, upstream)
	v := info.Versions[0]
	if !v.Published.Equal(feed) || !v.NugetOrgPublished.Equal(public) || len(info.Versions) != 1 {
		t.Fatalf("publication sources lost: %#v", v)
	}
	if age := versionAge(v); !strings.HasPrefix(age, "nuget.org: ") || strings.Contains(age, "today") {
		t.Fatalf("upstream age lacks provenance: %q", age)
	}
}
