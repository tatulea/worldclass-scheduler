package worldclass

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	defaultBaseURL = "https://members.worldclass.ro"
	defaultTZ      = "Europe/Bucharest"
)

// Config captures runtime settings loaded from config.yaml.
type Config struct {
	BaseURL     string
	Timezone    string
	Credentials Credentials
	Clubs       []Club
	Interests   map[string][]ClassInterest
}

// ClassInterest describes a class the user is interested in tracking or booking.
type ClassInterest struct {
	Day   string `yaml:"day"`
	Time  string `yaml:"time"`
	Title string `yaml:"title"`
	// DayEnglish should be the English weekday name (e.g., "Monday") used for scheduling calculations.
	DayEnglish string `yaml:"day_english"`
}

// LoadConfig reads the YAML configuration file from disk.
func LoadConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()

	cfg := &Config{
		BaseURL:   defaultBaseURL,
		Timezone:  defaultTZ,
		Interests: make(map[string][]ClassInterest),
	}

	scanner := bufio.NewScanner(file)

	var (
		section             string
		currentClub         Club
		currentClubActive   bool
		currentInterestClub string
		currentInterest     *ClassInterest
	)

	flushClub := func() {
		if currentClubActive {
			cfg.Clubs = append(cfg.Clubs, currentClub)
			currentClub = Club{}
			currentClubActive = false
		}
	}

	flushInterest := func() {
		if currentInterest != nil && currentInterestClub != "" {
			cfg.Interests[currentInterestClub] = append(cfg.Interests[currentInterestClub], *currentInterest)
			currentInterest = nil
		}
	}

	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		raw := scanner.Text()
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		if indent == 0 {
			flushInterest()
			flushClub()

			key, value, err := parseKeyValue(trimmed)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNumber, err)
			}

			switch key {
			case "base_url":
				if value != "" {
					cfg.BaseURL = value
				}
				section = ""
			case "timezone":
				if value != "" {
					cfg.Timezone = value
				}
				section = ""
			case "credentials", "clubs", "interests":
				section = key
			default:
				return nil, fmt.Errorf("line %d: unknown top-level key %q", lineNumber, key)
			}

			continue
		}

		switch section {
		case "credentials":
			key, value, err := parseKeyValue(trimmed)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNumber, err)
			}

			switch key {
			case "email":
				cfg.Credentials.Email = value
			case "password":
				cfg.Credentials.Password = value
			default:
				return nil, fmt.Errorf("line %d: unknown credentials key %q", lineNumber, key)
			}

		case "clubs":
			content := strings.TrimSpace(trimmed)
			if strings.HasPrefix(content, "- ") {
				flushClub()
				currentClubActive = true
				currentClub = Club{}

				content = strings.TrimSpace(strings.TrimPrefix(content, "-"))
				if content != "" {
					key, value, err := parseKeyValue(content)
					if err != nil {
						return nil, fmt.Errorf("line %d: %w", lineNumber, err)
					}
					if err := setClubField(&currentClub, key, value); err != nil {
						return nil, fmt.Errorf("line %d: %w", lineNumber, err)
					}
				}
				continue
			}

			if !currentClubActive {
				return nil, fmt.Errorf("line %d: club fields must follow a list item", lineNumber)
			}

			key, value, err := parseKeyValue(content)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNumber, err)
			}
			if err := setClubField(&currentClub, key, value); err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNumber, err)
			}

		case "interests":
			if indent == 2 && !strings.HasPrefix(trimmed, "- ") {
				flushInterest()
				currentInterestClub = strings.TrimSuffix(trimmed, ":")
				currentInterestClub = strings.Trim(currentInterestClub, "\"")
				if currentInterestClub == "" {
					return nil, fmt.Errorf("line %d: interest club name missing", lineNumber)
				}
				if _, ok := cfg.Interests[currentInterestClub]; !ok {
					cfg.Interests[currentInterestClub] = nil
				}
				continue
			}

			itemLine := strings.TrimSpace(trimmed)
			if strings.HasPrefix(itemLine, "- ") {
				flushInterest()
				currentInterest = &ClassInterest{}
				itemLine = strings.TrimSpace(strings.TrimPrefix(itemLine, "-"))
				if itemLine != "" {
					key, value, err := parseKeyValue(itemLine)
					if err != nil {
						return nil, fmt.Errorf("line %d: %w", lineNumber, err)
					}
					if err := setInterestField(currentInterest, key, value); err != nil {
						return nil, fmt.Errorf("line %d: %w", lineNumber, err)
					}
				}
				continue
			}

			if currentInterest == nil {
				return nil, fmt.Errorf("line %d: interest attributes must follow a list item", lineNumber)
			}

			key, value, err := parseKeyValue(itemLine)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNumber, err)
			}
			if err := setInterestField(currentInterest, key, value); err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNumber, err)
			}

		default:
			return nil, fmt.Errorf("line %d: unexpected content %q", lineNumber, trimmed)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	flushInterest()
	flushClub()

	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}

	if cfg.Timezone == "" {
		cfg.Timezone = defaultTZ
	}

	if cfg.Credentials.Email == "" || cfg.Credentials.Password == "" {
		return nil, errors.New("credentials.email and credentials.password must be set")
	}

	if len(cfg.Clubs) == 0 {
		return nil, errors.New("at least one club must be configured")
	}

	if cfg.Interests == nil {
		cfg.Interests = make(map[string][]ClassInterest)
	}

	return cfg, nil
}

func parseKeyValue(line string) (string, string, error) {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("expected key: value pair, got %q", line)
	}

	key := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])
	value = strings.Trim(value, "\"")

	return key, value, nil
}

func setClubField(club *Club, key, value string) error {
	switch key {
	case "id":
		club.ID = value
	case "name":
		club.Name = value
	default:
		return fmt.Errorf("unknown club field %q", key)
	}
	return nil
}

func setInterestField(ci *ClassInterest, key, value string) error {
	switch key {
	case "day":
		ci.Day = value
	case "day_english":
		ci.DayEnglish = value
	case "time":
		ci.Time = value
	case "title":
		ci.Title = value
	default:
		return fmt.Errorf("unknown interest field %q", key)
	}
	return nil
}
