package awsclient

import legacyaws "github.com/EliLillyCo/work-dashboard/internal/aws"

func IsCredentialError(err error) bool {
	return legacyaws.IsCredentialError(err)
}

func DerefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
