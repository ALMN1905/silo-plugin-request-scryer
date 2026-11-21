// Package seasonmap matches the season numbers Silo asks for (TMDB's) to the
// seasons Scryer has, which for some titles are split or merged differently.
package seasonmap

import (
	"slices"
	"time"
)

// Season is one season of a title as one catalog lists it.
type Season struct {
	Number   int
	Start    time.Time // first air date; zero when unknown
	Episodes int
}

// tolerance absorbs the day or two by which catalogs date the same episode.
const tolerance = 14 * 24 * time.Hour

// Map returns the Scryer season numbers that hold what Silo asked for.
//
// A TMDB season covers the Scryer seasons that start from its first air date
// up to the start of the next TMDB season. The last TMDB season has no next
// one, so it takes Scryer seasons in order until it has as many episodes as
// TMDB says it has (always at least one). A requested season that cannot be
// matched, because a date is unknown or no Scryer season falls in its range,
// keeps its own number.
func Map(requested []int32, tmdb, scryer []Season) []int32 {
	tmdb = slices.Clone(tmdb)
	slices.SortFunc(tmdb, func(a, b Season) int { return a.Number - b.Number })
	scryer = slices.Clone(scryer)
	slices.SortFunc(scryer, func(a, b Season) int { return a.Number - b.Number })

	var out []int32
	add := func(n int32) {
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	for _, r := range requested {
		matched := false
		if t, ok := find(tmdb, int(r)); ok && !t.Start.IsZero() {
			end := nextStart(tmdb, t)
			total := 0
			for _, s := range scryer {
				if s.Start.IsZero() || s.Start.Before(t.Start.Add(-tolerance)) {
					continue
				}
				if !end.IsZero() && !s.Start.Before(end.Add(-tolerance)) {
					continue
				}
				if end.IsZero() && total > 0 && total >= t.Episodes {
					break
				}
				add(int32(s.Number))
				matched = true
				total += s.Episodes
			}
		}
		if !matched {
			add(r)
		}
	}
	slices.Sort(out)
	return out
}

func find(seasons []Season, number int) (Season, bool) {
	for _, s := range seasons {
		if s.Number == number {
			return s, true
		}
	}
	return Season{}, false
}

// nextStart is the first air date of the next dated TMDB season, or zero.
func nextStart(tmdb []Season, after Season) time.Time {
	for _, s := range tmdb {
		if s.Number > after.Number && !s.Start.IsZero() {
			return s.Start
		}
	}
	return time.Time{}
}
