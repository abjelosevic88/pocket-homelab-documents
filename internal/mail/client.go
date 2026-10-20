// Package mail is a thin IMAP client for the mail rules: it signs in, lists
// folders, finds candidate messages, downloads them whole and applies the
// after-consume action (seen, flagged, moved, deleted). Parsing the messages
// and matching them against a rule are pure functions in this package so
// they can be tested without a server.
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// Security is how the connection is protected: ssl (implicit TLS), starttls, none.
const (
	SecuritySSL      = "ssl"
	SecurityStartTLS = "starttls"
	SecurityNone     = "none"
)

// ValidSecurity reports whether s is one of the known modes.
func ValidSecurity(s string) bool {
	return s == SecuritySSL || s == SecurityStartTLS || s == SecurityNone
}

// DefaultPort is the usual port for a security mode.
func DefaultPort(security string) int {
	if security == SecuritySSL {
		return 993
	}
	return 143
}

// Client wraps one IMAP connection.
type Client struct {
	c       *imapclient.Client
	timeout time.Duration
}

// Dial connects to host:port. The context bounds the connection attempt;
// once connected, commands use the client's own timeout.
func Dial(ctx context.Context, host string, port int, security string) (*Client, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, errors.New("no mail server given")
	}
	if port <= 0 {
		port = DefaultPort(security)
	}
	if !ValidSecurity(security) {
		return nil, fmt.Errorf("unknown security mode %q", security)
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	timeout := 30 * time.Second
	if dl, ok := ctx.Deadline(); ok {
		if d := time.Until(dl); d < timeout && d > 0 {
			timeout = d
		}
	}
	opts := &imapclient.Options{
		Dialer:    &net.Dialer{Timeout: timeout},
		TLSConfig: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12},
	}
	var (
		c   *imapclient.Client
		err error
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		switch security {
		case SecuritySSL:
			c, err = imapclient.DialTLS(addr, opts)
		case SecurityStartTLS:
			c, err = imapclient.DialStartTLS(addr, opts)
		default:
			c, err = imapclient.DialInsecure(addr, opts)
		}
	}()
	select {
	case <-done:
	case <-ctx.Done():
		go func() {
			<-done
			if c != nil {
				c.Close()
			}
		}()
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", addr, err)
	}
	return &Client{c: c, timeout: timeout}, nil
}

// Login signs in with a user name and password.
func (c *Client) Login(username, password string) error {
	if err := c.c.Login(username, password).Wait(); err != nil {
		return fmt.Errorf("sign in as %s: %w", username, err)
	}
	return nil
}

// Close logs out and drops the connection.
func (c *Client) Close() {
	if c == nil || c.c == nil {
		return
	}
	_ = c.c.Logout().Wait()
	_ = c.c.Close()
}

// Folders lists every mailbox, sorted, INBOX first.
func (c *Client) Folders() ([]string, error) {
	boxes, err := c.c.List("", "*", nil).Collect()
	if err != nil {
		return nil, fmt.Errorf("list folders: %w", err)
	}
	out := make([]string, 0, len(boxes))
	for _, b := range boxes {
		skip := false
		for _, a := range b.Attrs {
			if a == imap.MailboxAttrNoSelect {
				skip = true
			}
		}
		if !skip {
			out = append(out, b.Mailbox)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if strings.EqualFold(out[i], "INBOX") != strings.EqualFold(out[j], "INBOX") {
			return strings.EqualFold(out[i], "INBOX")
		}
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out, nil
}

// Select opens a folder and returns its UIDVALIDITY.
func (c *Client) Select(folder string) (uint32, error) {
	if strings.TrimSpace(folder) == "" {
		folder = "INBOX"
	}
	data, err := c.c.Select(folder, nil).Wait()
	if err != nil {
		return 0, fmt.Errorf("open folder %s: %w", folder, err)
	}
	return data.UIDValidity, nil
}

// Search returns the UIDs of messages in the selected folder: unseen ones
// only when unseenOnly, received since the date when since is set.
func (c *Client) Search(unseenOnly bool, since time.Time) ([]uint32, error) {
	crit := &imap.SearchCriteria{}
	if unseenOnly {
		crit.NotFlag = append(crit.NotFlag, imap.FlagSeen)
	}
	if !since.IsZero() {
		crit.Since = since
	}
	data, err := c.c.UIDSearch(crit, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	uids := data.AllUIDs()
	out := make([]uint32, 0, len(uids))
	for _, u := range uids {
		out = append(out, uint32(u))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

var wholeMessage = &imap.FetchItemBodySection{Peek: true}

// Fetch downloads the given messages whole (without marking them seen) and
// parses them. Messages that fail to parse are skipped.
func (c *Client) Fetch(uids []uint32) ([]*Message, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	set := imap.UIDSet{}
	for _, u := range uids {
		set.AddNum(imap.UID(u))
	}
	bufs, err := c.c.Fetch(set, &imap.FetchOptions{UID: true, Envelope: true, BodySection: []*imap.FetchItemBodySection{wholeMessage}}).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	out := make([]*Message, 0, len(bufs))
	for _, b := range bufs {
		raw := b.FindBodySection(wholeMessage)
		if len(raw) == 0 {
			continue
		}
		m, err := Parse(raw)
		if err != nil {
			continue
		}
		m.UID = uint32(b.UID)
		if env := b.Envelope; env != nil {
			if m.MessageID == "" {
				m.MessageID = env.MessageID
			}
			if m.Subject == "" {
				m.Subject = env.Subject
			}
			if m.Date.IsZero() {
				m.Date = env.Date
			}
			if m.From.Address == "" && len(env.From) > 0 {
				m.From = Address{Name: env.From[0].Name, Address: env.From[0].Addr()}
			}
		}
		out = append(out, m)
	}
	return out, nil
}

func (c *Client) store(uid uint32, op imap.StoreFlagsOp, flags ...imap.Flag) error {
	return c.c.Store(imap.UIDSetNum(imap.UID(uid)), &imap.StoreFlags{Op: op, Silent: true, Flags: flags}, nil).Close()
}

// MarkSeen sets \Seen.
func (c *Client) MarkSeen(uid uint32) error { return c.store(uid, imap.StoreFlagsAdd, imap.FlagSeen) }

// Flag sets \Flagged.
func (c *Client) Flag(uid uint32) error { return c.store(uid, imap.StoreFlagsAdd, imap.FlagFlagged) }

// Delete marks the message \Deleted and expunges it.
func (c *Client) Delete(uid uint32) error {
	if err := c.store(uid, imap.StoreFlagsAdd, imap.FlagDeleted); err != nil {
		return err
	}
	set := imap.UIDSetNum(imap.UID(uid))
	if c.c.Caps().Has(imap.CapUIDPlus) {
		return c.c.UIDExpunge(set).Close()
	}
	return c.c.Expunge().Close()
}

// Move moves the message to another folder: MOVE when the server has it,
// else COPY + delete + expunge.
func (c *Client) Move(uid uint32, folder string) error {
	if strings.TrimSpace(folder) == "" {
		return errors.New("no folder to move to")
	}
	set := imap.UIDSetNum(imap.UID(uid))
	if c.c.Caps().Has(imap.CapMove) {
		_, err := c.c.Move(set, folder).Wait()
		return err
	}
	if _, err := c.c.Copy(set, folder).Wait(); err != nil {
		return err
	}
	return c.Delete(uid)
}

// Test signs in and lists the folders: what the settings page calls.
func Test(ctx context.Context, host string, port int, security, username, password string) ([]string, error) {
	c, err := Dial(ctx, host, port, security)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if err := c.Login(username, password); err != nil {
		return nil, err
	}
	return c.Folders()
}
