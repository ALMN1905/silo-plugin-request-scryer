package seasonmap

import (
	"slices"
	"testing"
	"time"
)

func d(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestMap(t *testing.T) {
	for name, tc := range map[string]struct {
		requested []int32
		tmdb      []Season
		scryer    []Season
		want      []int32
	}{
		"same numbering": {
			[]int32{2},
			[]Season{{1, d("2022-01-09"), 12}, {2, d("2025-01-06"), 12}},
			[]Season{{1, d("2022-01-09"), 12}, {2, d("2025-01-06"), 12}},
			[]int32{2},
		},
		"TMDB merges two Scryer seasons": {
			[]int32{1},
			[]Season{{1, d("2022-01-09"), 24}},
			[]Season{{1, d("2022-01-09"), 12}, {2, d("2025-01-06"), 12}},
			[]int32{1, 2},
		},
		"TMDB one season of 60, Scryer 24+24+8": {
			[]int32{1},
			[]Season{{1, d("2023-10-22"), 60}},
			[]Season{{1, d("2023-10-22"), 24}, {2, d("2025-01-10"), 24}, {3, d("2025-10-04"), 8}},
			[]int32{1, 2, 3},
		},
		"last TMDB season stops once it has its episodes": {
			[]int32{1},
			[]Season{{1, d("2022-09-21"), 8}},
			[]Season{{1, d("2022-09-21"), 10}, {2, d("2024-10-03"), 9}, {3, d("2025-10-01"), 8}},
			[]int32{1},
		},
		"TMDB splits what Scryer keeps in one season": {
			[]int32{2},
			[]Season{{1, d("2024-01-01"), 12}, {2, d("2024-07-01"), 12}},
			[]Season{{1, d("2024-01-02"), 24}},
			[]int32{2}, // Scryer has no season in that range of its own: the number is kept
		},
		"earlier TMDB season ends where the next starts": {
			[]int32{1},
			[]Season{{1, d("2020-01-01"), 10}, {2, d("2021-01-01"), 10}},
			[]Season{{1, d("2020-01-03"), 5}, {2, d("2020-06-01"), 5}, {3, d("2021-01-02"), 10}},
			[]int32{1, 2},
		},
		"upcoming season without a Scryer season keeps its number": {
			[]int32{2},
			[]Season{{1, d("2023-08-22"), 8}, {2, d("2027-01-19"), 8}},
			[]Season{{1, d("2023-08-22"), 8}},
			[]int32{2},
		},
		"unknown TMDB date keeps the number": {
			[]int32{3},
			[]Season{{1, d("2023-08-22"), 8}, {3, time.Time{}, 0}},
			[]Season{{1, d("2023-08-22"), 8}},
			[]int32{3},
		},
		"several requested seasons, no duplicates": {
			[]int32{1, 2},
			[]Season{{1, d("2020-01-01"), 10}, {2, d("2021-01-01"), 10}},
			[]Season{{1, d("2020-01-01"), 10}, {2, d("2021-01-01"), 10}},
			[]int32{1, 2},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := Map(tc.requested, tc.tmdb, tc.scryer); !slices.Equal(got, tc.want) {
				t.Fatalf("Map = %v, want %v", got, tc.want)
			}
		})
	}
}
