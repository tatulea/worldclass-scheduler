package worldclass

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"
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

// RunSchedule attempts to reserve every class that matches the configured interests.
func RunSchedule(cfg *Config, args []string) error {
	if cfg == nil {
		return fmt.Errorf("configuration is required")
	}

	scheduleCmd := flag.NewFlagSet("schedule", flag.ExitOnError)
	if err := scheduleCmd.Parse(args); err != nil {
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

	classes = filterClassesForInterests(classes, cfg.Interests, logf)
	if len(classes) == 0 {
		logf("no classes matched your filters")
		return nil
	}

	var bookSession *bookingSession

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
			continue
		case !classInfo.Bookable:
			logf(
				"Booking not open yet: %s | %s | %s | Trainer: %s | Title: %s",
				classInfo.ClubName,
				classInfo.Day,
				classInfo.Time,
				classInfo.Trainer,
				classInfo.Title,
			)
			continue
		}

		if classInfo.ClassID == "" {
			logf("Skipping %s | %s | %s | %s: missing class identifier", classInfo.ClubName, classInfo.Day, classInfo.Time, classInfo.Title)
			continue
		}

		if classInfo.ClubID == "" {
			logf("Skipping %s | %s | %s | %s: missing club identifier", classInfo.ClubName, classInfo.Day, classInfo.Time, classInfo.Title)
			continue
		}

		if bookSession == nil {
			bookSession, err = client.newBookingSession(ctx, cfg.Credentials)
			if err != nil {
				return fmt.Errorf("start booking session: %w", err)
			}
		}

		logf(
			"Scheduling attempt: %s | %s | %s | %s | ClassID: %s",
			classInfo.ClubName,
			classInfo.Day,
			classInfo.Time,
			classInfo.Title,
			classInfo.ClassID,
		)

		success, err := bookSession.BookClass(ctx, classInfo.ClubID, classInfo.ClassID)
		if err != nil {
			logf(
				"Failed booking: %s | %s | %s | %s | error: %v",
				classInfo.ClubName,
				classInfo.Day,
				classInfo.Time,
				classInfo.Title,
				err,
			)
			continue
		}

		if success {
			logf(
				"Booked successfully: %s | %s | %s | %s | ClassID: %s",
				classInfo.ClubName,
				classInfo.Day,
				classInfo.Time,
				classInfo.Title,
				classInfo.ClassID,
			)
			continue
		}

		logf(
			"Booking attempted but not confirmed: %s | %s | %s | %s | ClassID: %s",
			classInfo.ClubName,
			classInfo.Day,
			classInfo.Time,
			classInfo.Title,
			classInfo.ClassID,
		)
	}

	return nil
}

func logf(format string, args ...interface{}) {
	now := time.Now().Format(time.DateTime)
	fmt.Printf("[%s] %s\n", now, fmt.Sprintf(format, args...))
}

// filterClassesForInterests keeps only the classes that satisfy at least one configured interest per club.
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

// interestMatches reports whether the scraped class satisfies the provided interest criteria.
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
		logger("interest for %s on %s %s has no title filter; matching class %q", classInfo.ClubName, classInfo.Day, classInfo.Time, classInfo.Title)
		return true
	}

	titleNeedle := strings.ToLower(titleNeedleRaw)
	classTitle := strings.ToLower(strings.TrimSpace(classInfo.Title))

	if strings.EqualFold(strings.TrimSpace(classInfo.Title), titleNeedleRaw) {
		return true
	}

	if strings.Contains(classTitle, titleNeedle) {
		logger("interest title %q partially matched class title %q for %s on %s %s", interest.Title, classInfo.Title, classInfo.ClubName, classInfo.Day, classInfo.Time)
		return true
	}

	return false
}
