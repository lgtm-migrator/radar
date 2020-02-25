package radar

import (
	"context"
	"fmt"
	"net/http"
	"net/mail"
	"time"

	"mvdan.cc/xurls/v2"
)

type RadarItemsStorageService interface {
	// Store a new radar item.
	Create(ctx context.Context, m RadarItem) error
	// Delete a radar item by numerical id.
	Delete(ctx context.Context, id int64) error
	// Get a radar item by numerical id.
	Get(ctx context.Context, id int64) (RadarItem, error)
	// List radar items by numerical id.
	List(ctx context.Context, limit int) ([]RadarItem, error)
	// Shut down the storage service gracefully.
	Shutdown(ctx context.Context)
}

func NewEmailHandler(radarItemsService RadarItemsStorageService, mailgunService MailgunService, allowedSenders []string, debug bool) EmailHandler {
	return EmailHandler{
		AllowedSenders: allowedSenders,
		Debug:          debug,
		RadarItems:     radarItemsService,
		Mailgun:        mailgunService,
		CreateQueue:    make(chan createRequest, 10),
	}
}

type EmailHandler struct {
	// Email addresses that must be in the "From" section of the message.
	AllowedSenders []string

	// Enable debug logging.
	Debug bool

	// RadarItem service
	RadarItems RadarItemsStorageService

	// Mailgun service, used for sending email replies
	Mailgun MailgunService

	// The queue
	CreateQueue chan createRequest
}

type createRequest struct {
	fromEmail string

	messageID string

	subject string

	url string
}

// Start polls on the CreateQueue and runs
func (h EmailHandler) Start() {
	for req := range h.CreateQueue {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := h.RadarItems.Create(ctx, RadarItem{URL: req.url}); err != nil {
			Printf("error saving '%s': %#v %+v", req.url, err, err)
			h.Mailgun.SendReply(req, "Could not save "+req.url+" to the radar: "+err.Error())
		} else {
			h.Mailgun.SendReply(req, "Added "+req.url+" to the radar.")
			Printf("saved url=%s to database", req.url)
		}
		cancel()
	}
}

func (h EmailHandler) Shutdown(ctx context.Context) {
	close(h.CreateQueue)
	h.RadarItems.Shutdown(ctx)
}

func (h EmailHandler) IsAllowedSender(sender string) bool {
	email, err := mail.ParseAddress(sender)
	if err != nil {
		Printf("could not process sender '%s': %#v", sender, err)
		return false
	}

	for _, allowedSender := range h.AllowedSenders {
		if allowedSender == email.Address {
			return true
		}
	}

	return false
}

func (h EmailHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if contentType := r.Header.Get("Content-Type"); contentType != "application/x-www-form-urlencoded" {
		Println("don't know how to handle Content-Type:", contentType)
		http.Error(w, "cannot process Content-Type: "+contentType, http.StatusBadRequest)
		return
	}

	if sender := r.FormValue("From"); !h.IsAllowedSender(sender) {
		Println("not an allowed sender: ", sender)
		http.Error(w, "not an allowed sender: "+sender, http.StatusUnauthorized)
		return
	}

	emailBody := r.FormValue("body-plain")
	if h.Debug {
		Printf("body-plain: %#v", emailBody)
	}

	var urls []string
	if matches := xurls.Strict().FindAllString(emailBody, -1); matches != nil && len(matches) > 0 {
		urls = append(urls, matches...)
	}

	if len(urls) == 0 {
		Println("no urls in body: ", emailBody)
		http.Error(w, "no urls present in email body", http.StatusOK)
		return
	}

	if h.Debug {
		Printf("urls: %#v", urls)
		Printf("form: %#v", r.Form)
	}

	for _, url := range urls {
		h.CreateQueue <- createRequest{
			fromEmail: r.FormValue("From"),
			messageID: r.FormValue("Message-Id"),
			subject:   r.FormValue("Subject"),
			url:       url,
		}
	}

	http.Error(w, fmt.Sprintf("added %d urls to today's radar", len(urls)), http.StatusCreated)
}
