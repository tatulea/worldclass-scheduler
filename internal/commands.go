package worldclass

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
)

const (
	bookingLeadTime    = 26 * time.Hour
	bookingEarlyBuffer = 1 * time.Minute
	bookingRetryDelay  = 10 * time.Second
	bookingGracePeriod = 1 * time.Minute
	idleLoopDelay      = time.Hour
)

// RunFetch executes the fetch workflow, optionally filtering classes against the configured interests.
func RunFetch(cfg *Config, args []string) error {
	if cfg == nil {
		return fmt.Errorf("configuration is required")
	}

	fetchCmd := flag.NewFlagSet("fetch", flag.ExitOnError)
	showAll := fetchCmd.Bool("all", false, "show every class regardless of configured interests")
	if err := fetchCmd.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	client, err := NewWorldClassClient(cfg.BaseURL, logf)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	classes, err := client.FetchClasses(ctx, cfg.Credentials, cfg.Clubs)
	if err != nil {
		return err
	}

	if !*showAll {
		classes = filterClassesForInterests(classes, cfg.Interests, logf)
	}

	if len(classes) == 0 {
		logf("no classes matched your filters")
		return nil
	}

	for _, classInfo := range classes {
		switch {
		case classInfo.Booked:
			logf(
				"Already booked: %s | %s | %s | %s | Trainer: %s | ClassID: %s",
				classInfo.ClubName,
				classInfo.Day,
				classInfo.Time,
				classInfo.Title,
				classInfo.Trainer,
				classInfo.ClassID,
			)
		case classInfo.Bookable:
			logf(
				"Bookable: %s | %s | %s | %s | Trainer: %s | ClassID: %s",
				classInfo.ClubName,
				classInfo.Day,
				classInfo.Time,
				classInfo.Title,
				classInfo.Trainer,
				classInfo.ClassID,
			)
		default:
			logf(
				"Scheduled (booking closed): %s | %s | %s | Trainer: %s | Title: %s",
				classInfo.ClubName,
				classInfo.Day,
				classInfo.Time,
				classInfo.Trainer,
				classInfo.Title,
			)
		}
	}

	return nil
}

// RunSchedule attempts to reserve classes that match the configured interests.
func RunSchedule(cfg *Config, args []string) error {
	if cfg == nil {
		return fmt.Errorf("configuration is required")
	}

	scheduleCmd := flag.NewFlagSet("schedule", flag.ExitOnError)
	loop := scheduleCmd.Bool("loop", false, "continuously monitor and book upcoming classes")
	if err := scheduleCmd.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if *loop {
		return runScheduleLoop(cfg)
	}

	return runScheduleOnce(cfg)
}

func runScheduleOnce(cfg *Config) error {
	client, err := NewWorldClassClient(cfg.BaseURL, logf)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err = scheduleInterests(ctx, client, cfg, cfg.Interests)
	return err
}

func runScheduleLoop(cfg *Config) error {
	location, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return fmt.Errorf("load timezone %s: %w", cfg.Timezone, err)
	}

	client, err := NewWorldClassClient(cfg.BaseURL, logf)
	if err != nil {
		return err
	}

	sentryEnabled, err := initSentry(cfg.Sentry.DSN)
	if err != nil {
		return err
	}

	if sentryEnabled {
		defer sentry.Flush(5 * time.Second)
		defer func() {
			if r := recover(); r != nil {
				sentry.CurrentHub().Recover(r)
				sentry.Flush(5 * time.Second)
				panic(r)
			}
		}()
	}

	for {
		now := time.Now().In(location)
		handle, startTime, err := nextInterestOccurrence(cfg, location, now)
		if err != nil {
			if errors.Is(err, errNoInterests) {
				logf("no interests configured; sleeping for %s", idleLoopDelay)
				time.Sleep(idleLoopDelay)
				continue
			}
			reportLoopError(sentryEnabled, err, map[string]string{"phase": "next_interest"})
			return err
		}

		wakeTime := startTime.Add(-bookingLeadTime).Add(-bookingEarlyBuffer)
		if wakeTime.After(time.Now()) {
			logf("Next class %s | %s | %s scheduled for %s, waking at %s", handle.Club, handle.Interest.Day, handle.Interest.Time, startTime.Format(time.RFC1123), wakeTime.Format(time.RFC1123))
			time.Sleep(time.Until(wakeTime))
		} else {
			logf("Booking window already open for %s | %s | %s, attempting immediately", handle.Club, handle.Interest.Day, handle.Interest.Time)
		}

		deadline := startTime.Add(bookingGracePeriod)
		for {
			if time.Now().After(deadline) {
				logf("Unable to book %s | %s | %s before cutoff; will retry next occurrence", handle.Club, handle.Interest.Day, handle.Interest.Time)
				break
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			results, err := scheduleInterests(ctx, client, cfg, map[string][]ClassInterest{handle.Club: {handle.Interest}})
			cancel()
			if err != nil {
				logf("Scheduling attempt failed: %v", err)
				reportLoopError(sentryEnabled, err, map[string]string{
					"phase": "booking",
					"club":  handle.Club,
					"title": handle.Interest.Title,
				})
			} else if interestSatisfied(handle, results) {
				break
			}

			time.Sleep(bookingRetryDelay)
		}
	}
}

func interestSatisfied(handle *scheduledInterest, results []interestResult) bool {
	for _, res := range results {
		if res.ClubName == handle.Club && interestsEqual(res.Interest, handle.Interest) {
			return res.Status == statusBooked || res.Status == statusAlreadyBooked
		}
	}
	return false
}

func initSentry(dsn string) (bool, error) {
	if dsn == "" {
		return false, nil
	}

	if err := sentry.Init(sentry.ClientOptions{Dsn: dsn}); err != nil {
		return false, fmt.Errorf("sentry init: %w", err)
	}

	return true, nil
}

func reportLoopError(enabled bool, err error, extras map[string]string) {
	if !enabled || err == nil {
		return
	}

	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("mode", "loop")
		for k, v := range extras {
			scope.SetTag(k, v)
		}
		sentry.CaptureException(err)
	})
}

func scheduleInterests(ctx context.Context, client *WorldClassClient, cfg *Config, interests map[string][]ClassInterest) ([]interestResult, error) {
	classes, err := client.FetchClasses(ctx, cfg.Credentials, cfg.Clubs)
	if err != nil {
		return nil, err
	}

	results := make([]interestResult, 0)
	var bookSession *bookingSession
	matches := 0

	for _, clubName := range sortedKeys(interests) {
		for _, interest := range interests[clubName] {
			res := interestResult{ClubName: clubName, Interest: interest, Status: statusNoMatch}
			classInfo, found := findMatchingClass(classes, clubName, interest)
			if !found {
				results = append(results, res)
				continue
			}

			matches++

			switch {
			case classInfo.Booked:
				logf("Already booked: %s | %s | %s | %s | Trainer: %s | ClassID: %s", classInfo.ClubName, classInfo.Day, classInfo.Time, classInfo.Title, classInfo.Trainer, classInfo.ClassID)
				res.Status = statusAlreadyBooked
				results = append(results, res)
				continue
			case !classInfo.Bookable:
				logf("Booking not open yet: %s | %s | %s | Trainer: %s | Title: %s", classInfo.ClubName, classInfo.Day, classInfo.Time, classInfo.Trainer, classInfo.Title)
				res.Status = statusNotOpen
				results = append(results, res)
				continue
			}

			if classInfo.ClassID == "" {
				logf("Skipping %s | %s | %s | %s: missing class identifier", classInfo.ClubName, classInfo.Day, classInfo.Time, classInfo.Title)
				res.Status = statusMissingData
				results = append(results, res)
				continue
			}

			if classInfo.ClubID == "" {
				logf("Skipping %s | %s | %s | %s: missing club identifier", classInfo.ClubName, classInfo.Day, classInfo.Time, classInfo.Title)
				res.Status = statusMissingData
				results = append(results, res)
				continue
			}

			if bookSession == nil {
				bookSession, err = client.newBookingSession(ctx, cfg.Credentials)
				if err != nil {
					return nil, fmt.Errorf("start booking session: %w", err)
				}
			}

			logf("Scheduling attempt: %s | %s | %s | %s | ClassID: %s", classInfo.ClubName, classInfo.Day, classInfo.Time, classInfo.Title, classInfo.ClassID)

			success, err := bookSession.BookClass(ctx, classInfo.ClubID, classInfo.ClassID)
			if err != nil {
				logf("Failed booking: %s | %s | %s | %s | error: %v", classInfo.ClubName, classInfo.Day, classInfo.Time, classInfo.Title, err)
				res.Status = statusBookingFailed
				results = append(results, res)
				continue
			}

			if success {
				logf("Booked successfully: %s | %s | %s | %s | ClassID: %s", classInfo.ClubName, classInfo.Day, classInfo.Time, classInfo.Title, classInfo.ClassID)
				res.Status = statusBooked
			} else {
				logf("Booking attempted but not confirmed: %s | %s | %s | %s | ClassID: %s", classInfo.ClubName, classInfo.Day, classInfo.Time, classInfo.Title, classInfo.ClassID)
				res.Status = statusBookingFailed
			}

			results = append(results, res)
		}
	}

	if matches == 0 {
		logf("no classes matched your filters")
	}

	return results, nil
}

func logf(format string, args ...interface{}) {
	now := time.Now().Format(time.DateTime)
	fmt.Printf("[%s] %s\n", now, fmt.Sprintf(format, args...))
}

func filterClassesForInterests(classes []Class, interests map[string][]ClassInterest, logger func(string, ...interface{})) []Class {
	if logger == nil {
		logger = func(string, ...interface{}) {}
	}

	var filtered []Class
	for _, classInfo := range classes {
		interestList, ok := interests[classInfo.ClubName]
		if !ok || len(interestList) == 0 {
			continue
		}

		for _, interest := range interestList {
			if interestMatches(classInfo, interest, logger) {
				filtered = append(filtered, classInfo)
				break
			}
		}
	}

	return filtered
}

func interestMatches(classInfo Class, interest ClassInterest, logger func(string, ...interface{})) bool {
	normalizedDay := strings.ToLower(strings.TrimSpace(classInfo.Day))
	dayNeedle := strings.ToLower(strings.TrimSpace(interest.Day))
	if dayNeedle != "" && !strings.Contains(normalizedDay, dayNeedle) {
		return false
	}

	normalizedTime := strings.TrimSpace(classInfo.Time)
	timeNeedle := strings.TrimSpace(interest.Time)
	if timeNeedle != "" && !strings.EqualFold(normalizedTime, timeNeedle) {
		return false
	}

	titleNeedleRaw := strings.TrimSpace(interest.Title)
	if titleNeedleRaw == "" {
		if logger != nil {
			logger("interest for %s on %s %s has no title filter; matching class %q", classInfo.ClubName, classInfo.Day, classInfo.Time, classInfo.Title)
		}
		return true
	}

	titleNeedle := strings.ToLower(titleNeedleRaw)
	classTitle := strings.ToLower(strings.TrimSpace(classInfo.Title))

	if strings.EqualFold(strings.TrimSpace(classInfo.Title), titleNeedleRaw) {
		return true
	}

	if strings.Contains(classTitle, titleNeedle) {
		if logger != nil {
			logger("interest title %q partially matched class title %q for %s on %s %s", interest.Title, classInfo.Title, classInfo.ClubName, classInfo.Day, classInfo.Time)
		}
		return true
	}

	return false
}

type interestStatus int

const (
	statusNoMatch interestStatus = iota
	statusAlreadyBooked
	statusNotOpen
	statusBooked
	statusBookingFailed
	statusMissingData
)

type interestResult struct {
	ClubName string
	Interest ClassInterest
	Status   interestStatus
}

type scheduledInterest struct {
	Club     string
	Interest ClassInterest
}

var errNoInterests = errors.New("no class interests configured")

func findMatchingClass(classes []Class, clubName string, interest ClassInterest) (Class, bool) {
	for _, classInfo := range classes {
		if classInfo.ClubName != clubName {
			continue
		}
		if interestMatches(classInfo, interest, nil) {
			return classInfo, true
		}
	}
	return Class{}, false
}

func nextInterestOccurrence(cfg *Config, loc *time.Location, reference time.Time) (*scheduledInterest, time.Time, error) {
	var (
		nextHandle *scheduledInterest
		nextTime   time.Time
	)

	for _, clubName := range sortedKeys(cfg.Interests) {
		for _, interest := range cfg.Interests[clubName] {
			weekday, err := parseWeekday(interest.DayEnglish)
			if err != nil {
				return nil, time.Time{}, fmt.Errorf("parse weekday for %s (%s): %w", clubName, interest.Title, err)
			}

			hour, minute, err := parseStartTime(interest.Time)
			if err != nil {
				return nil, time.Time{}, fmt.Errorf("parse time for %s (%s): %w", clubName, interest.Title, err)
			}

			occurrence := computeNextOccurrence(reference, loc, weekday, hour, minute)
			if nextHandle == nil || occurrence.Before(nextTime) {
				nextHandle = &scheduledInterest{Club: clubName, Interest: interest}
				nextTime = occurrence
			}
		}
	}

	if nextHandle == nil {
		return nil, time.Time{}, errNoInterests
	}

	return nextHandle, nextTime, nil
}

func parseWeekday(day string) (time.Weekday, error) {
	switch strings.ToLower(strings.TrimSpace(day)) {
	case "sunday":
		return time.Sunday, nil
	case "monday":
		return time.Monday, nil
	case "tuesday":
		return time.Tuesday, nil
	case "wednesday":
		return time.Wednesday, nil
	case "thursday":
		return time.Thursday, nil
	case "friday":
		return time.Friday, nil
	case "saturday":
		return time.Saturday, nil
	default:
		return time.Sunday, fmt.Errorf("unknown weekday %q", day)
	}
}

func parseStartTime(raw string) (int, int, error) {
	parts := strings.Split(raw, "-")
	if len(parts) == 0 {
		return 0, 0, fmt.Errorf("invalid time format %q", raw)
	}

	start := strings.TrimSpace(parts[0])
	hm := strings.Split(start, ":")
	if len(hm) < 2 {
		return 0, 0, fmt.Errorf("invalid start time %q", start)
	}

	hour, err := strconv.Atoi(strings.TrimSpace(hm[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid hour in %q: %w", start, err)
	}

	minute, err := strconv.Atoi(strings.TrimSpace(hm[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid minute in %q: %w", start, err)
	}

	return hour, minute, nil
}

func computeNextOccurrence(reference time.Time, loc *time.Location, weekday time.Weekday, hour, minute int) time.Time {
	start := time.Date(reference.Year(), reference.Month(), reference.Day(), hour, minute, 0, 0, loc)
	for start.Before(reference) || start.Weekday() != weekday {
		start = start.Add(24 * time.Hour)
	}
	return start
}

func sortedKeys(m map[string][]ClassInterest) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func interestsEqual(a, b ClassInterest) bool {
	return a.Day == b.Day &&
		a.DayEnglish == b.DayEnglish &&
		a.Time == b.Time &&
		a.Title == b.Title
}
