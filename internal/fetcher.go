package worldclass

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gocolly/colly"
)

type Credentials struct {
	Email    string `yaml:"email"`
	Password string `yaml:"password"`
}

type Club struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
}

type Class struct {
	ClubID   string
	ClubName string
	Day      string
	Time     string
	Title    string
	Trainer  string
	Room     string
	ClassID  string
	Bookable bool
	Booked   bool
}

// WorldClassClient wraps the scraping and booking interactions with the World Class member site.
type WorldClassClient struct {
	baseURL *url.URL
	logger  func(format string, args ...interface{})
}

type bookingSession struct {
	client  *http.Client
	baseURL *url.URL
}

// NewWorldClassClient creates a configured client that targets the provided base URL.
func NewWorldClassClient(rawBaseURL string, logger func(format string, args ...interface{})) (*WorldClassClient, error) {
	if rawBaseURL == "" {
		return nil, errors.New("base URL is required")
	}

	parsedURL, err := url.Parse(rawBaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}

	if logger == nil {
		logger = func(string, ...interface{}) {}
	}

	return &WorldClassClient{
		baseURL: parsedURL,
		logger:  logger,
	}, nil
}

// FetchClasses scrapes the club schedule pages and returns every class that matches the provided clubs.
func (c *WorldClassClient) FetchClasses(ctx context.Context, creds Credentials, clubs []Club) ([]Class, error) {
	if c == nil || c.baseURL == nil {
		return nil, errors.New("world class client is not initialised")
	}

	if creds.Email == "" || creds.Password == "" {
		return nil, errors.New("email and password are required")
	}

	if len(clubs) == 0 {
		return nil, errors.New("at least one club is required")
	}

	collector := colly.NewCollector(
		colly.AllowedDomains(c.baseURL.Host),
	)
	collector.SetRequestTimeout(15 * time.Second)
	collector.WithTransport(&http.Transport{
		Proxy: http.ProxyFromEnvironment,
	})

	var (
		classes   []Class
		classesMu sync.Mutex
	)

	collector.OnRequest(func(r *colly.Request) {
		select {
		case <-ctx.Done():
			r.Abort()
			return
		default:
		}
	})

	collector.OnError(func(r *colly.Response, err error) {
		c.logger("request to %s failed: %v", r.Request.URL.String(), err)
	})

	collector.OnHTML(".daily-schedule", func(e *colly.HTMLElement) {
		clubName := e.Request.Ctx.Get("clubName")
		if clubName == "" {
			clubName = "Unknown club"
		}
		clubID := e.Request.Ctx.Get("clubID")

		day := e.ChildText("div.schedule-day>strong")
		e.ForEach(".schedule-class", func(_ int, el *colly.HTMLElement) {
			classButton := el.DOM.Find(".btn-book-class")
			hasBookButton := len(classButton.Nodes) > 0
			alreadyBooked := false
			if hasBookButton {
				alreadyBooked = classButton.HasClass("cancel-link")
			}

			classID := el.ChildAttr("div.col-xs-5.col-sm-12.text-right>a", "data-target")
			classID = strings.TrimPrefix(classID, "#")
			classID = strings.TrimPrefix(classID, "class-")

			classInfo := Class{
				ClubID:   clubID,
				ClubName: clubName,
				Day:      day,
				Time:     el.ChildText("div.col-xs-7.col-sm-12>span.class-hours"),
				Room:     el.ChildText("div.col-xs-7.col-sm-12>span.room"),
				Title:    el.ChildText("div.col-xs-7.col-sm-12>strong.class-title"),
				Trainer:  el.ChildText("div.col-xs-7.col-sm-12>span.trainers"),
				ClassID:  classID,
				Bookable: hasBookButton && !alreadyBooked,
				Booked:   alreadyBooked,
			}

			classesMu.Lock()
			classes = append(classes, classInfo)
			classesMu.Unlock()
		})
	})

	loginURL := c.baseURL.JoinPath("_process_login.php")
	if err := collector.Post(loginURL.String(), map[string]string{
		"email":           creds.Email,
		"member_password": creds.Password,
		"remember_me":     "false",
	}); err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}

	scheduleURL := c.baseURL.JoinPath("member-schedule.php")
	for _, club := range clubs {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		form := url.Values{}
		form.Set("clubid", club.ID)
		form.Set("group", "-1")

		reqCtx := colly.NewContext()
		reqCtx.Put("clubName", club.Name)
		reqCtx.Put("clubID", club.ID)
		if err := collector.Request(
			http.MethodPost,
			scheduleURL.String(),
			strings.NewReader(form.Encode()),
			reqCtx,
			http.Header{
				"Content-Type": []string{"application/x-www-form-urlencoded"},
			},
		); err != nil {
			return nil, fmt.Errorf("request schedule for club %s (%s): %w", club.Name, club.ID, err)
		}
	}

	collector.Wait()

	return classes, nil
}

// newBookingSession authenticates with the member site and prepares a client that can submit booking requests.
func (c *WorldClassClient) newBookingSession(ctx context.Context, creds Credentials) (*bookingSession, error) {
	if creds.Email == "" || creds.Password == "" {
		return nil, errors.New("email and password are required")
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("create cookie jar: %w", err)
	}

	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	loginURL := c.baseURL.JoinPath("_process_login.php")
	form := url.Values{
		"email":           {creds.Email},
		"member_password": {creds.Password},
		"remember_me":     {"false"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("login request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		return nil, fmt.Errorf("login failed: expected redirect, got status %d", resp.StatusCode)
	}

	expected := c.baseURL.JoinPath("dashboard.php").String()
	if loc := normalizeLocation(c.baseURL, resp.Header.Get("Location")); loc != expected {
		return nil, fmt.Errorf("login failed: unexpected redirect to %s", loc)
	}

	return &bookingSession{
		client:  client,
		baseURL: c.baseURL,
	}, nil
}

// BookClass attempts to reserve a class via the booking endpoint and reports whether the operation succeeded.
func (s *bookingSession) BookClass(ctx context.Context, clubID, classID string) (bool, error) {
	if s == nil || s.client == nil || s.baseURL == nil {
		return false, errors.New("booking session is not initialised")
	}

	if clubID == "" || classID == "" {
		return false, errors.New("clubID and classID are required")
	}

	scheduleURL := s.baseURL.JoinPath("_book_class.php")
	query := url.Values{}
	query.Set("id", classID)
	query.Set("clubid", clubID)
	scheduleURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, scheduleURL.String(), nil)
	if err != nil {
		return false, fmt.Errorf("build booking request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("booking request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusFound {
		loc := normalizeLocation(s.baseURL, resp.Header.Get("Location"))
		if loc == s.baseURL.JoinPath("member-schedule.php").String() {
			return true, nil
		}

		return false, fmt.Errorf("booking rejected, redirected to %s", loc)
	}

	if resp.StatusCode == http.StatusOK {
		// Some responses might not redirect but still indicate success.
		return true, nil
	}

	return false, fmt.Errorf("booking unexpected status %d", resp.StatusCode)
}

// normalizeLocation resolves redirect locations against the base URL, producing absolute URLs for logging and comparisons.
func normalizeLocation(base *url.URL, loc string) string {
	if base == nil || loc == "" {
		return loc
	}

	ref, err := url.Parse(loc)
	if err != nil {
		return loc
	}

	return base.ResolveReference(ref).String()
}
