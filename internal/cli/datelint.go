package cli

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Relative-date lint for `ask new` (act-d54808). An ask is read days or weeks
// after it is filed, so "tomorrow" in its title stops meaning anything the day
// after — hygiene passes found asks titled "Tomorrow: …" sitting for 63 days.
// The lint warns at filing time, when the filer still knows what the word
// meant, and resolves it against the filing moment so the warning can name the
// absolute date to write instead. It never refuses: a filer quoting someone
// ("he said 'next week'") is legitimate.

var (
	reRelWord    = regexp.MustCompile(`(?i)\b(today|tonight|tomorrow|yesterday|this morning|this afternoon|this evening|last night|this week|next week|last week|this weekend|next weekend)\b`)
	reInNDays    = regexp.MustCompile(`(?i)\bin (\d{1,3}) days?\b`)
	reNDaysAgo   = regexp.MustCompile(`(?i)\b(\d{1,3}) days? ago\b`)
	reWeekday    = regexp.MustCompile(`(?i)\b(monday|tuesday|wednesday|thursday|friday|saturday|sunday)\b`)
	reNearbyDate = regexp.MustCompile(`(?i)\d{4}-\d{2}-\d{2}|\b\d{1,2}/\d{1,2}\b|\b(jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)[a-z]*\.? \d{1,2}\b|\b\d{1,2} (jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)`)
	reEvery      = regexp.MustCompile(`(?i)\b(every|each)\s+$`)
)

// weekdayDateWindow is how close (in bytes) an absolute date must sit to a
// weekday name for the weekday to count as anchored ("Monday 2026-09-28",
// "Mon, Sep 28").
const weekdayDateWindow = 24

// relativeDateWarnings returns one human-readable warning per distinct
// relative-date phrase in text, each naming the absolute date it resolves to
// at now (the filing moment, in the filer's local zone).
func relativeDateWarnings(text string, now time.Time) []string {
	seen := map[string]bool{}
	var out []string
	add := func(phrase, suggestion string) {
		key := strings.ToLower(phrase)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, fmt.Sprintf("%q is relative and will read wrong once this ask has sat a while; write the date instead (%s)", phrase, suggestion))
	}
	day := func(offset int) string { return now.AddDate(0, 0, offset).Format("2006-01-02") }
	weekOf := func(offsetDays int) string {
		d := now.AddDate(0, 0, offsetDays)
		// Monday of that week.
		back := (int(d.Weekday()) + 6) % 7
		return "the week of " + d.AddDate(0, 0, -back).Format("2006-01-02")
	}

	for _, m := range reRelWord.FindAllString(text, -1) {
		switch strings.ToLower(m) {
		case "today", "tonight", "this morning", "this afternoon", "this evening":
			add(m, m+" = "+day(0))
		case "tomorrow":
			add(m, m+" = "+day(1))
		case "yesterday", "last night":
			add(m, m+" = "+day(-1))
		case "this week", "this weekend":
			add(m, m+" = "+weekOf(0))
		case "next week", "next weekend":
			add(m, m+" = "+weekOf(7))
		case "last week":
			add(m, m+" = "+weekOf(-7))
		}
	}
	for _, sm := range reInNDays.FindAllStringSubmatch(text, -1) {
		n, _ := strconv.Atoi(sm[1])
		add(sm[0], sm[0]+" = "+day(n))
	}
	for _, sm := range reNDaysAgo.FindAllStringSubmatch(text, -1) {
		n, _ := strconv.Atoi(sm[1])
		add(sm[0], sm[0]+" = "+day(-n))
	}
	for _, loc := range reWeekday.FindAllStringIndex(text, -1) {
		name := text[loc[0]:loc[1]]
		// "every Monday", "on Mondays" — a recurring day, not a date.
		if loc[1] < len(text) && (text[loc[1]] == 's' || text[loc[1]] == 'S') {
			continue
		}
		if reEvery.MatchString(text[max(0, loc[0]-8):loc[0]]) {
			continue
		}
		lo, hi := max(0, loc[0]-weekdayDateWindow), min(len(text), loc[1]+weekdayDateWindow)
		if reNearbyDate.MatchString(text[lo:hi]) {
			continue
		}
		add(name, "which "+name+"? e.g. "+nextWeekday(now, name))
	}
	return out
}

// nextWeekday names the next occurrence of weekday (strictly after today) as
// "Monday 2026-09-28", the form the warning suggests.
func nextWeekday(now time.Time, weekday string) string {
	for i := 1; i <= 7; i++ {
		d := now.AddDate(0, 0, i)
		if strings.EqualFold(d.Weekday().String(), weekday) {
			return d.Weekday().String() + " " + d.Format("2006-01-02")
		}
	}
	return weekday
}
