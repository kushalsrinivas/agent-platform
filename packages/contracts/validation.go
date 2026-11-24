package contracts

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ValidationError lists every contract violation found in one pass.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return "contract violation:\n  " + strings.Join(e.Problems, "\n  ")
}

type validator struct {
	problems []string
}

func (v *validator) addf(format string, args ...any) {
	v.problems = append(v.problems, fmt.Sprintf(format, args...))
}

func (v *validator) err() error {
	if len(v.problems) == 0 {
		return nil
	}
	return &ValidationError{Problems: v.problems}
}

func (v *validator) requiredString(field, value string, minLen, maxLen int) {
	if value == "" {
		v.addf("%s is required", field)
		return
	}
	if n := utf8.RuneCountInString(value); n < minLen || n > maxLen {
		v.addf("%s must be %d..%d characters", field, minLen, maxLen)
	}
}

func (v *validator) pattern(field, value string, re *regexp.Regexp) {
	if !re.MatchString(value) {
		v.addf("%s %q has an invalid format", field, value)
	}
}

func (v *validator) enumDefault(field, value string, allowed ...string) {
	if value == "" {
		return
	}
	v.enum(field, value, allowed...)
}

func (v *validator) enum(field, value string, allowed ...string) {
	for _, candidate := range allowed {
		if value == candidate {
			return
		}
	}
	v.addf("%s %q must be one of %s", field, value, strings.Join(allowed, ", "))
}

var (
	orgIDPattern             = regexp.MustCompile(`^org_[A-Za-z0-9_-]{8,64}$`)
	projectIDPattern         = regexp.MustCompile(`^proj_[A-Za-z0-9_-]{8,64}$`)
	workflowIDPattern        = regexp.MustCompile(`^wf_[A-Za-z0-9_-]{8,64}$`)
	workflowVersionIDPattern = regexp.MustCompile(`^wfv_[A-Za-z0-9_-]{8,64}$`)
	agentIDPattern           = regexp.MustCompile(`^agt_[A-Za-z0-9_-]{8,64}$`)
	agentVersionIDPattern    = regexp.MustCompile(`^agtv_[A-Za-z0-9_-]{8,64}$`)
	executionIDPattern       = regexp.MustCompile(`^exec_[A-Za-z0-9_-]{8,64}$`)
	taskIDPattern            = regexp.MustCompile(`^task_[A-Za-z0-9_-]{8,64}$`)
	attemptIDPattern         = regexp.MustCompile(`^att_[A-Za-z0-9_-]{8,64}$`)
	messageIDPattern         = regexp.MustCompile(`^msg_[A-Za-z0-9_-]{8,64}$`)
	workerIDPattern          = regexp.MustCompile(`^wrk_[A-Za-z0-9_-]{8,64}$`)
	nodeIDPattern            = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	digestPattern            = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	traceparentPattern       = regexp.MustCompile(`^[0-9a-f]{2}-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`)
)
