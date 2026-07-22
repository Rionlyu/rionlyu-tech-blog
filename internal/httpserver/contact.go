package httpserver

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Rionlyu/rionlyu-tech-blog/internal/contact"
	"github.com/Rionlyu/rionlyu-tech-blog/internal/render"
)

var allowedContactFields = map[string]struct{}{
	"_csrf":   {},
	"name":    {},
	"email":   {},
	"message": {},
	"website": {},
}

func (s *Server) submitContact(w http.ResponseWriter, r *http.Request) {
	fragment := isHTMX(r)
	_, hadValidCSRFCookie := s.validCSRFCookie(r)
	token, err := s.ensureCSRF(w, r)
	if err != nil {
		s.renderFailure(w, r, err)
		return
	}

	mediaType, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/x-www-form-urlencoded" {
		s.renderContact(w, r, http.StatusBadRequest, render.ContactForm{
			CSRFToken:   token,
			Errors:      map[string]string{},
			Available:   s.config.ContactProvider != "disabled",
			GlobalError: s.site.Pages.Contact.InvalidSubmission,
		}, fragment)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.config.ContactMaxBody)
	if err := r.ParseForm(); err != nil {
		var maxBytesError *http.MaxBytesError
		message := s.site.Pages.Contact.InvalidSubmission
		status := http.StatusBadRequest
		if errors.As(err, &maxBytesError) {
			message = s.site.Pages.Contact.RequestTooLarge
			status = http.StatusRequestEntityTooLarge
		}
		s.renderContact(w, r, status, render.ContactForm{
			CSRFToken:   token,
			Errors:      map[string]string{},
			Available:   s.config.ContactProvider != "disabled",
			GlobalError: message,
		}, fragment)
		return
	}

	if !s.validateCSRF(r, r.PostForm.Get("_csrf")) {
		rotated := token
		if hadValidCSRFCookie {
			var rotateErr error
			rotated, rotateErr = s.rotateCSRF(w)
			if rotateErr != nil {
				s.renderFailure(w, r, rotateErr)
				return
			}
		}
		s.renderContact(w, r, http.StatusForbidden, render.ContactForm{
			CSRFToken:   rotated,
			Name:        safeName(r.PostForm.Get("name")),
			Errors:      map[string]string{},
			Available:   s.config.ContactProvider != "disabled",
			GlobalError: s.site.Pages.Contact.FormExpired,
		}, fragment)
		return
	}

	if !validContactShape(r.PostForm) {
		s.renderContact(w, r, http.StatusBadRequest, render.ContactForm{
			CSRFToken:   token,
			Name:        safeName(r.PostForm.Get("name")),
			Errors:      map[string]string{},
			Available:   s.config.ContactProvider != "disabled",
			GlobalError: s.site.Pages.Contact.InvalidSubmission,
		}, fragment)
		return
	}

	if strings.TrimSpace(r.PostForm.Get("website")) != "" {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	message, fieldErrors := contact.Validate(contact.Message{
		Name:  r.PostForm.Get("name"),
		Email: r.PostForm.Get("email"),
		Body:  r.PostForm.Get("message"),
	})
	if fieldErrors.Any() {
		s.renderContact(w, r, http.StatusUnprocessableEntity, render.ContactForm{
			CSRFToken: token,
			Name:      message.Name,
			Errors:    map[string]string(fieldErrors),
			Available: s.config.ContactProvider != "disabled",
		}, fragment)
		return
	}

	if s.config.ContactProvider == "disabled" {
		s.renderContact(w, r, http.StatusServiceUnavailable, render.ContactForm{
			CSRFToken:   token,
			Name:        message.Name,
			Errors:      map[string]string{},
			Available:   false,
			GlobalError: s.site.Pages.Contact.Unavailable,
		}, fragment)
		return
	}

	if allowed, retryAfter := s.limiter.Allow(s.rateKey(r)); !allowed {
		seconds := int64((retryAfter + time.Second - 1) / time.Second)
		w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
		s.renderContact(w, r, http.StatusTooManyRequests, render.ContactForm{
			CSRFToken:   token,
			Name:        message.Name,
			Errors:      map[string]string{},
			Available:   true,
			GlobalError: s.site.Pages.Contact.RateLimited,
		}, fragment)
		return
	}

	successID := requestIDFrom(r.Context())
	if successID == "" {
		successID, _ = s.randomString(12)
	}
	successData, err := s.contactBytes(r, render.ContactForm{
		Available: true,
		Success:   true,
		SuccessID: successID,
		Errors:    map[string]string{},
	}, fragment)
	if err != nil {
		s.renderFailure(w, r, err)
		return
	}

	deliveryContext, cancel := context.WithTimeout(r.Context(), s.config.ContactTimeout)
	defer cancel()
	if err := s.sender.Send(deliveryContext, message); err != nil {
		s.logger.WarnContext(r.Context(), "contact delivery failed",
			"request_id", requestIDFrom(r.Context()),
			"error", safeProviderError(err),
		)
		s.renderContact(w, r, http.StatusBadGateway, render.ContactForm{
			CSRFToken:   token,
			Name:        message.Name,
			Errors:      map[string]string{},
			Available:   true,
			GlobalError: s.site.Pages.Contact.DeliveryFailed,
		}, fragment)
		return
	}

	s.writeContact(w, http.StatusOK, successData, fragment)
}

func validContactShape(form map[string][]string) bool {
	for key, values := range form {
		if _, allowed := allowedContactFields[key]; !allowed || len(values) != 1 {
			return false
		}
	}
	for _, required := range []string{"_csrf", "name", "email", "message", "website"} {
		if len(form[required]) != 1 {
			return false
		}
	}
	return true
}

func safeName(value string) string {
	name, fieldErrors := contact.Validate(contact.Message{Name: value, Email: "valid@example.test", Body: "placeholder"})
	if _, invalid := fieldErrors["name"]; invalid || len([]rune(name.Name)) > contact.MaxNameRunes {
		return ""
	}
	return name.Name
}

func safeProviderError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "delivery timeout"
	}
	return fmt.Sprintf("provider error (%T)", err)
}
