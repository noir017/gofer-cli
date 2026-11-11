// Package extract finds verification codes and links in a message.
package extract

import "github.com/noir017/gofer-cli/internal/model"

// OTP returns the verification code in m. A non-empty pattern is a regexp
// whose first group (or whole match) is the code.
func OTP(m *model.Message, pattern string) (string, bool) { return "", false }

// Link returns the verification or sign-in link in m. A non-empty match is a
// regexp the link must match.
func Link(m *model.Message, match string) (string, bool) { return "", false }
