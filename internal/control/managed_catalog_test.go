package control

import "testing"

func TestManagedCatalogAcceptsStationGroupsAndSortOrder(t *testing.T) {
	for _, group := range []string{"image", "video", "music", "desktop", "system", "channel"} {
		input := ManagedAppInput{Name: "目录应用", GroupID: group, Channel: "Candidate", SortOrder: 1000}
		if err := validateManagedApp(input); err != nil {
			t.Errorf("group %q: %v", group, err)
		}
	}
	for _, order := range []int{-1, 1000001} {
		input := ManagedAppInput{Name: "目录应用", GroupID: "system", Channel: "Candidate", SortOrder: order}
		if err := validateManagedApp(input); err == nil {
			t.Errorf("sort order %d should be rejected", order)
		}
	}
}
