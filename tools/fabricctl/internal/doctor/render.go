package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func Render(w io.Writer, report Report, output string) error {
	if output == "json" {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	return renderHuman(w, report)
}

func renderHuman(w io.Writer, report Report) error {
	if _, err := fmt.Fprintf(w, "SingleAxis Fabric preflight\nProfile: %s  Namespace: %s\n\n", report.Profile, report.Namespace); err != nil {
		return err
	}
	for _, check := range report.Results {
		marker := strings.ToUpper(string(check.Status))
		if _, err := fmt.Fprintf(w, "[%s] %-34s %s\n", marker, check.Code, check.Summary); err != nil {
			return err
		}
		if check.Status != StatusPass && check.Remediation != "" {
			if _, err := fmt.Fprintf(w, "       Remediation: %s\n", check.Remediation); err != nil {
				return err
			}
		}
		for _, evidence := range check.Evidence {
			if _, err := fmt.Fprintf(w, "       Evidence: %s\n", evidence); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintf(w, "\nSummary: %d passed, %d warnings, %d failed, %d skipped; %d required failures\n",
		report.Summary.Passed, report.Summary.Warnings, report.Summary.Failed, report.Summary.Skipped, report.Summary.FailedRequired)
	return err
}
