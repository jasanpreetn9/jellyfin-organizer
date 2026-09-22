package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"jellyfin-organizer/internal/jellyfin"
)

type Action string

const (
	ActionSetAbsolute Action = "set_absolute"
	ActionTagFiller   Action = "tag_filler"
	ActionUntagFiller Action = "untag_filler"
	ActionBoth        Action = "both" // set absolute index + tag fillers in one pass
)

func (a Action) Valid() bool {
	switch a {
	case ActionSetAbsolute, ActionTagFiller, ActionUntagFiller, ActionBoth:
		return true
	}
	return false
}

// Job is one unit of work: an action applied to every episode of a series.
type Job struct {
	UserID       string `json:"userId"`
	SeriesID     string `json:"seriesId"`
	SeriesName   string `json:"seriesName"`
	Action       Action `json:"action"`
	FillerRanges string `json:"fillerRanges"`
	// SkipRanges lists absolute episode numbers that must never be
	// modified by this job (no index change, no title change).
	SkipRanges string `json:"skipRanges"`
}

type Options struct {
	DryRun     bool
	NFORefresh bool
}

// Logf receives progress lines; level is "info", "change", "warn" or "error".
type Logf func(level, message string)

// SortEpisodes orders by (season, episode), treating missing numbers as 0.
func SortEpisodes(eps []jellyfin.Episode) {
	deref := func(p *int) int {
		if p == nil {
			return 0
		}
		return *p
	}
	sort.SliceStable(eps, func(i, j int) bool {
		si, sj := deref(eps[i].Season), deref(eps[j].Season)
		if si != sj {
			return si < sj
		}
		return deref(eps[i].Index) < deref(eps[j].Index)
	})
}

// EpisodeLabel renders "S02E21" style labels for log lines.
func EpisodeLabel(ep jellyfin.Episode) string {
	s, e := 0, 0
	if ep.Season != nil {
		s = *ep.Season
	}
	if ep.Index != nil {
		e = *ep.Index
	}
	return fmt.Sprintf("S%02dE%02d", s, e)
}

// RunJob executes one job against the Jellyfin server, reporting every
// change (or would-be change, in dry-run) through logf.
// Returns the number of episodes changed (or that would change).
func RunJob(ctx context.Context, c *jellyfin.Client, job Job, opt Options, logf Logf) (int, error) {
	if !job.Action.Valid() {
		return 0, fmt.Errorf("unknown action %q", job.Action)
	}
	needFiller := job.Action == ActionTagFiller || job.Action == ActionBoth
	fillerSet := map[int]bool{}
	if needFiller {
		var err error
		fillerSet, err = ParseRanges(job.FillerRanges)
		if err != nil {
			return 0, fmt.Errorf("filler ranges: %w", err)
		}
		if len(fillerSet) == 0 {
			logf("warn", "no filler episode numbers given — nothing to tag")
		}
	}
	skipSet, err := ParseRanges(job.SkipRanges)
	if err != nil {
		return 0, fmt.Errorf("skip ranges: %w", err)
	}

	eps, err := c.Episodes(ctx, job.SeriesID)
	if err != nil {
		return 0, err
	}
	SortEpisodes(eps)
	logf("info", fmt.Sprintf("%d episode(s) fetched", len(eps)))

	changed, skipped := 0, 0
	for _, ep := range eps {
		if ctx.Err() != nil {
			return changed, ctx.Err()
		}
		abs := ExtractAbsoluteNumber(ep.Path)
		label := EpisodeLabel(ep)

		if abs > 0 && skipSet[abs] {
			skipped++
			continue
		}
		if abs == 0 && job.Action != ActionUntagFiller {
			logf("warn", fmt.Sprintf("%s: could not parse absolute episode number from %q", label, BaseName(ep.Path)))
		}

		newIndex := 0 // 0 = no index change
		if (job.Action == ActionSetAbsolute || job.Action == ActionBoth) && abs > 0 {
			if ep.Index == nil || *ep.Index != abs {
				newIndex = abs
			}
		}

		newTitle := "" // "" = no title change
		switch job.Action {
		case ActionTagFiller, ActionBoth:
			if abs > 0 && fillerSet[abs] {
				if t := AddFillerPrefix(ep.Name); t != ep.Name {
					newTitle = t
				}
			}
		case ActionUntagFiller:
			if t := RemoveFillerTag(ep.Name); t != ep.Name {
				newTitle = t
			}
		}

		if newIndex == 0 && newTitle == "" {
			continue
		}

		var parts []string
		if newIndex != 0 {
			old := "—"
			if ep.Index != nil {
				old = fmt.Sprintf("E%02d", *ep.Index)
			}
			parts = append(parts, fmt.Sprintf("index %s → E%02d", old, newIndex))
		}
		if newTitle != "" {
			parts = append(parts, fmt.Sprintf("title %q → %q", ep.Name, newTitle))
		}
		msg := fmt.Sprintf("%s (abs %03d): %s", label, abs, strings.Join(parts, "; "))

		if opt.DryRun {
			logf("change", "[dry-run] "+msg)
			changed++
			continue
		}

		item, err := c.ItemForEdit(ctx, job.UserID, ep.ID)
		if err != nil {
			logf("error", fmt.Sprintf("%s: fetch for edit failed: %v", label, err))
			continue
		}
		if newIndex != 0 {
			item["IndexNumber"] = newIndex
		}
		if newTitle != "" {
			item["Name"] = newTitle
		}
		if err := c.UpdateItem(ctx, ep.ID, item); err != nil {
			logf("error", fmt.Sprintf("%s: update failed: %v", label, err))
			continue
		}
		if newIndex != 0 && opt.NFORefresh {
			if err := c.RefreshItem(ctx, ep.ID); err != nil {
				logf("warn", fmt.Sprintf("%s: NFO refresh failed: %v", label, err))
			}
		}
		logf("change", msg)
		changed++
	}
	if skipped > 0 {
		logf("info", fmt.Sprintf("left %d episode(s) untouched (skip list)", skipped))
	}
	return changed, nil
}

// RenameEpisode sets a single episode's title.
func RenameEpisode(ctx context.Context, c *jellyfin.Client, userID, itemID, newTitle string) error {
	item, err := c.ItemForEdit(ctx, userID, itemID)
	if err != nil {
		return err
	}
	item["Name"] = newTitle
	return c.UpdateItem(ctx, itemID, item)
}
