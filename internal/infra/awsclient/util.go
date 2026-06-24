package awsclient

func DerefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
