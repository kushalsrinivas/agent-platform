package buildinfo

// Info is the public version document.
func Info() map[string]string {
	return map[string]string{
		"version":    Version,
		"commit":     Commit,
		"build_date": BuildDate,
	}
}
