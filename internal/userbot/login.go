package userbot

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
)

const (
	// qrMaxDuration caps a QR login flow even if the caller's ctx never ends.
	qrMaxDuration = 10 * time.Minute
	// phoneFlowTTL bounds how long a sent code / password step stays valid.
	phoneFlowTTL = 15 * time.Minute
)

// LoginStep is what the current login flow awaits.
type LoginStep string

// Login steps.
const (
	LoginNone     LoginStep = ""
	LoginQR       LoginStep = "qr"       // waiting for the QR code to be scanned
	LoginCode     LoginStep = "code"     // waiting for SubmitCode
	LoginPassword LoginStep = "password" // waiting for SubmitPassword
)

// CodeInfo describes how Telegram delivered the login code.
type CodeInfo struct {
	// Type is one of "app", "sms", "call", "flash_call", "missed_call",
	// "email", "fragment_sms", "firebase_sms", "sms_word", "sms_phrase",
	// "setup_email", or "unknown".
	Type string
	// Length of the code, 0 if unknown.
	Length int
	// Timeout after which NextType may be used (0 if not provided).
	Timeout time.Duration
	// NextType is the delivery method of a resend ("" if none).
	NextType string
	// Self is non-nil when Telegram authorized the session without a code.
	Self *Self
}

// LoginResult is the outcome of a successful login step.
type LoginResult struct {
	// NeedPassword means two-step verification is enabled; call SubmitPassword.
	NeedPassword bool
	PasswordHint string
	// Self is set when the account is now logged in.
	Self *Self
}

// QRUpdate carries a freshly exported login token rendered as a QR code.
type QRUpdate struct {
	PNG     []byte
	URL     string // tg://login?token=...
	Expires time.Time
}

// QRResult is the final value of a QR login flow: either a LoginResult or Err.
type QRResult struct {
	LoginResult
	Err error
}

// QRLogin is a running QR login flow.
type QRLogin interface {
	// Updates emits a new QR code whenever the token rotates (~30s). Only
	// the latest code is buffered. Closed when the flow ends.
	Updates() <-chan QRUpdate
	// Result yields exactly one value when the flow ends.
	Result() <-chan QRResult
	// Cancel aborts the flow.
	Cancel()
}

// loginFlow is the state of the single active login flow (guarded by m.mu).
type loginFlow struct {
	id       uint64
	step     LoginStep
	phone    string
	codeHash string
	busy     bool
	expires  time.Time
	cancel   context.CancelFunc // QR goroutine, nil for phone flows
}

type qrFlow struct {
	updates chan QRUpdate
	result  chan QRResult
	cancel  func()
}

func (q *qrFlow) Updates() <-chan QRUpdate { return q.updates }
func (q *qrFlow) Result() <-chan QRResult  { return q.result }
func (q *qrFlow) Cancel()                  { q.cancel() }

// push delivers u, replacing a stale undelivered update.
func (q *qrFlow) push(u QRUpdate) {
	for {
		select {
		case q.updates <- u:
			return
		default:
		}
		select {
		case <-q.updates:
		default:
		}
	}
}

// beginFlow replaces the current login flow with a new one.
func (m *Manager) beginFlow(step LoginStep, cancel context.CancelFunc) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelLoginLocked()
	m.loginSeq++
	m.login = &loginFlow{
		id:      m.loginSeq,
		step:    step,
		busy:    step != LoginQR,
		expires: time.Now().Add(phoneFlowTTL),
		cancel:  cancel,
	}
	m.notifyLocked()
	return m.loginSeq
}

// withFlow runs fn on the flow if it is still the current one.
func (m *Manager) withFlow(id uint64, fn func(f *loginFlow)) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.login == nil || m.login.id != id {
		return false
	}
	fn(m.login)
	m.notifyLocked()
	return true
}

// endFlow removes the flow if it is still current.
func (m *Manager) endFlow(id uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.login != nil && m.login.id == id {
		if m.login.cancel != nil {
			m.login.cancel()
		}
		m.login = nil
		m.notifyLocked()
	}
}

// cancelLoginLocked aborts the current flow; m.mu must be held.
func (m *Manager) cancelLoginLocked() {
	f := m.login
	if f == nil {
		return
	}
	m.login = nil
	if f.cancel != nil {
		f.cancel()
	}
	if f.step == LoginCode && f.codeHash != "" && m.client != nil {
		// Best effort: invalidate the sent code.
		api := m.client.API()
		go func(phone, hash string) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = api.AuthCancelCode(ctx, &tg.AuthCancelCodeRequest{PhoneNumber: phone, PhoneCodeHash: hash})
		}(f.phone, f.codeHash)
	}
}

// CancelLogin aborts the current login flow, if any.
func (m *Manager) CancelLogin() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.login != nil {
		m.cancelLoginLocked()
		m.notifyLocked()
	}
}

// loginClient waits for a logged-out connection.
func (m *Manager) loginClient(ctx context.Context) (*telegram.Client, error) {
	client, st, err := m.waitConnected(ctx)
	if err != nil {
		return nil, err
	}
	if st == StateReady {
		return nil, ErrAlreadyLoggedIn
	}
	return client, nil
}

// StartQRLogin starts a QR login flow, cancelling any previous flow. ctx
// bounds the whole flow (capped at 10 minutes), not just this call. The user
// scans the code in Telegram → Settings → Devices → Link Desktop Device.
func (m *Manager) StartQRLogin(ctx context.Context) (QRLogin, error) {
	client, err := m.loginClient(ctx)
	if err != nil {
		return nil, err
	}
	fctx, cancel := context.WithTimeout(ctx, qrMaxDuration)
	id := m.beginFlow(LoginQR, cancel)
	q := &qrFlow{
		updates: make(chan QRUpdate, 1),
		result:  make(chan QRResult, 1),
		cancel:  func() { m.endFlow(id) },
	}
	// Drop a stale acceptance signal from an earlier flow.
	select {
	case <-m.loginToken:
	default:
	}
	go func() {
		defer cancel()
		defer close(q.updates)
		authz, err := m.qrAuth(fctx, client, q)
		q.result <- m.finishQR(fctx, client, id, authz, err)
	}()
	m.log.Info("qr login started")
	return q, nil
}

// qrAuth runs gotd's QR flow, rendering every token.
func (m *Manager) qrAuth(ctx context.Context, client *telegram.Client, q *qrFlow) (*tg.AuthAuthorization, error) {
	qr := client.QR()
	authz, err := qr.Auth(ctx, m.loginToken, func(_ context.Context, token qrlogin.Token) error {
		img, err := RenderQR(token.URL())
		if err != nil {
			return err
		}
		q.push(QRUpdate{PNG: img, URL: token.URL(), Expires: token.Expires()})
		return nil
	})
	// The token may have been accepted on another DC while we were rotating
	// it (export returned loginTokenMigrateTo); Import handles migration.
	var mig *qrlogin.MigrationNeededError
	if errors.As(err, &mig) {
		authz, err = qr.Import(ctx)
	}
	return authz, err
}

// finishQR converts the QR flow outcome into a QRResult and updates state.
func (m *Manager) finishQR(fctx context.Context, client *telegram.Client, id uint64, authz *tg.AuthAuthorization, err error) QRResult {
	if err == nil {
		self, err := m.completeLogin(id, authz)
		if err != nil {
			return QRResult{Err: err}
		}
		return QRResult{LoginResult: LoginResult{Self: self}}
	}
	if fctx.Err() != nil {
		m.endFlow(id)
		if errors.Is(fctx.Err(), context.DeadlineExceeded) {
			return QRResult{Err: ErrTimeout}
		}
		return QRResult{Err: ErrLoginCancelled}
	}
	mapped := m.observe(err)
	if errors.Is(mapped, errPasswordNeeded) {
		hint := m.passwordHint(fctx, client)
		ok := m.withFlow(id, func(f *loginFlow) {
			f.step = LoginPassword
			f.cancel = nil
			f.busy = false
			f.expires = time.Now().Add(phoneFlowTTL)
		})
		if !ok {
			return QRResult{Err: ErrLoginCancelled}
		}
		m.log.Info("qr login accepted, password required")
		return QRResult{LoginResult: LoginResult{NeedPassword: true, PasswordHint: hint}}
	}
	m.endFlow(id)
	m.log.Warn("qr login failed", "err", mapped)
	return QRResult{Err: mapped}
}

// StartPhoneLogin sends a login code to phone (auth.sendCode), cancelling any
// previous flow. Follow up with SubmitCode.
func (m *Manager) StartPhoneLogin(ctx context.Context, phone string) (CodeInfo, error) {
	digits, err := NormalizePhone(phone)
	if err != nil {
		return CodeInfo{}, err
	}
	client, err := m.loginClient(ctx)
	if err != nil {
		return CodeInfo{}, err
	}
	id := m.beginFlow(LoginCode, nil)
	m.log.Info("sending login code", "phone", MaskPhone(digits))

	sent, err := client.API().AuthSendCode(ctx, &tg.AuthSendCodeRequest{
		PhoneNumber: digits,
		APIID:       m.opts.APIID,
		APIHash:     m.opts.APIHash,
		Settings:    tg.CodeSettings{},
	})
	if err != nil {
		m.endFlow(id)
		return CodeInfo{}, m.observe(err)
	}
	switch s := sent.(type) {
	case *tg.AuthSentCode:
		ok := m.withFlow(id, func(f *loginFlow) {
			f.phone = digits
			f.codeHash = s.PhoneCodeHash
			f.busy = false
			f.expires = time.Now().Add(phoneFlowTTL)
		})
		if !ok {
			return CodeInfo{}, ErrLoginCancelled
		}
		return codeInfoFrom(s), nil
	case *tg.AuthSentCodeSuccess:
		a, ok := s.Authorization.(*tg.AuthAuthorization)
		if !ok {
			m.endFlow(id)
			return CodeInfo{}, ErrSignUpRequired
		}
		self, err := m.completeLogin(id, a)
		if err != nil {
			return CodeInfo{}, err
		}
		return CodeInfo{Type: "none", Self: self}, nil
	default:
		m.endFlow(id)
		return CodeInfo{}, fmt.Errorf("userbot: unsupported sent code response %T", sent)
	}
}

// SubmitCode completes a phone login with the received code. Separators in
// the code are ignored. Returns NeedPassword when 2FA is enabled; on
// ErrInvalidCode the flow stays open for another attempt.
func (m *Manager) SubmitCode(ctx context.Context, code string) (LoginResult, error) {
	code = NormalizeCode(code)
	var id uint64
	var phone, hash string
	var client *telegram.Client
	m.mu.Lock()
	f := m.login
	switch {
	case f == nil || f.step != LoginCode || f.codeHash == "":
		m.mu.Unlock()
		return LoginResult{}, ErrNoLogin
	case time.Now().After(f.expires):
		m.login = nil
		m.notifyLocked()
		m.mu.Unlock()
		return LoginResult{}, ErrCodeExpired
	case f.busy:
		m.mu.Unlock()
		return LoginResult{}, ErrLoginInProgress
	case m.client == nil:
		m.mu.Unlock()
		return LoginResult{}, ErrNotReady
	case code == "":
		m.mu.Unlock()
		return LoginResult{}, ErrInvalidCode
	}
	f.busy = true
	id, phone, hash, client = f.id, f.phone, f.codeHash, m.client
	m.mu.Unlock()
	release := func() { m.withFlow(id, func(f *loginFlow) { f.busy = false }) }

	a, err := client.API().AuthSignIn(ctx, &tg.AuthSignInRequest{
		PhoneNumber:   phone,
		PhoneCodeHash: hash,
		PhoneCode:     code,
	})
	if err != nil {
		mapped := m.observe(err)
		switch {
		case errors.Is(mapped, errPasswordNeeded):
			hint := m.passwordHint(ctx, client)
			ok := m.withFlow(id, func(f *loginFlow) {
				f.step = LoginPassword
				f.busy = false
				f.expires = time.Now().Add(phoneFlowTTL)
			})
			if !ok {
				return LoginResult{}, ErrLoginCancelled
			}
			m.log.Info("login code accepted, password required")
			return LoginResult{NeedPassword: true, PasswordHint: hint}, nil
		case errors.Is(mapped, ErrCodeExpired), errors.Is(mapped, ErrPhoneInvalid), errors.Is(mapped, ErrPhoneBanned):
			m.endFlow(id)
		default:
			release()
		}
		return LoginResult{}, mapped
	}
	switch v := a.(type) {
	case *tg.AuthAuthorization:
		self, err := m.completeLogin(id, v)
		if err != nil {
			return LoginResult{}, err
		}
		return LoginResult{Self: self}, nil
	default:
		m.endFlow(id)
		return LoginResult{}, ErrSignUpRequired
	}
}

// SubmitPassword completes the two-step verification step of either login
// flow. The password is used verbatim. On ErrPasswordInvalid the flow stays
// open for another attempt.
func (m *Manager) SubmitPassword(ctx context.Context, password string) (LoginResult, error) {
	var id uint64
	var client *telegram.Client
	m.mu.Lock()
	f := m.login
	switch {
	case f == nil || f.step != LoginPassword:
		m.mu.Unlock()
		return LoginResult{}, ErrNoLogin
	case time.Now().After(f.expires):
		m.login = nil
		m.notifyLocked()
		m.mu.Unlock()
		return LoginResult{}, ErrTimeout
	case f.busy:
		m.mu.Unlock()
		return LoginResult{}, ErrLoginInProgress
	case m.client == nil:
		m.mu.Unlock()
		return LoginResult{}, ErrNotReady
	case password == "":
		m.mu.Unlock()
		return LoginResult{}, ErrPasswordInvalid
	}
	f.busy = true
	id, client = f.id, m.client
	m.mu.Unlock()

	a, err := client.Auth().Password(ctx, password)
	if err != nil {
		m.withFlow(id, func(f *loginFlow) { f.busy = false })
		return LoginResult{}, m.observe(err)
	}
	self, err := m.completeLogin(id, a)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Self: self}, nil
}

// passwordHint fetches the 2FA hint (best effort).
func (m *Manager) passwordHint(ctx context.Context, client *telegram.Client) string {
	p, err := client.API().AccountGetPassword(ctx)
	if err != nil {
		return ""
	}
	return p.Hint
}

// completeLogin records a successful authorization.
func (m *Manager) completeLogin(id uint64, a *tg.AuthAuthorization) (*Self, error) {
	if a == nil {
		m.endFlow(id)
		return nil, errors.New("userbot: empty authorization")
	}
	u, ok := a.User.AsNotEmpty()
	if !ok {
		m.endFlow(id)
		return nil, fmt.Errorf("userbot: unexpected user %T", a.User)
	}
	self := selfFromUser(u)
	m.mu.Lock()
	if m.login != nil && m.login.id == id {
		m.login = nil
	}
	m.authorized = true
	m.authKnown = true
	m.self = self
	m.lastErr = ""
	m.notifyLocked()
	m.mu.Unlock()
	m.log.Info("user account logged in", "user_id", self.ID, "phone", self.PhoneMasked)
	out := *self
	return &out, nil
}

// Logout terminates the authorization (auth.logOut) and clears the stored
// session. When the account is connected and authorized, a failing
// auth.logOut aborts the logout so it can be retried; when offline only the
// local session is cleared (the authorization then remains listed under the
// account's active sessions).
func (m *Manager) Logout(ctx context.Context) error {
	m.CancelLogin()
	m.mu.Lock()
	client, authorized, adapter := m.client, m.authorized, m.adapter
	connected := m.connected
	m.mu.Unlock()

	if client != nil && authorized && connected {
		if _, err := client.API().AuthLogOut(ctx); err != nil && !isAuthLost(err) {
			return m.observe(err)
		}
	}
	m.mu.Lock()
	m.authorized = false
	m.self = nil
	m.webpagePeer = nil
	m.notifyLocked()
	m.mu.Unlock()
	if err := m.clearStore(ctx, adapter); err != nil {
		return err
	}
	m.requestRestart()
	m.log.Info("user account logged out")
	return nil
}

// codeInfoFrom describes a sent code.
func codeInfoFrom(s *tg.AuthSentCode) CodeInfo {
	info := CodeInfo{Type: "unknown"}
	switch t := s.Type.(type) {
	case *tg.AuthSentCodeTypeApp:
		info.Type, info.Length = "app", t.Length
	case *tg.AuthSentCodeTypeSMS:
		info.Type, info.Length = "sms", t.Length
	case *tg.AuthSentCodeTypeCall:
		info.Type, info.Length = "call", t.Length
	case *tg.AuthSentCodeTypeFlashCall:
		info.Type = "flash_call"
	case *tg.AuthSentCodeTypeMissedCall:
		info.Type, info.Length = "missed_call", t.Length
	case *tg.AuthSentCodeTypeEmailCode:
		info.Type, info.Length = "email", t.Length
	case *tg.AuthSentCodeTypeSetUpEmailRequired:
		info.Type = "setup_email"
	case *tg.AuthSentCodeTypeFragmentSMS:
		info.Type, info.Length = "fragment_sms", t.Length
	case *tg.AuthSentCodeTypeFirebaseSMS:
		info.Type, info.Length = "firebase_sms", t.Length
	case *tg.AuthSentCodeTypeSMSWord:
		info.Type = "sms_word"
	case *tg.AuthSentCodeTypeSMSPhrase:
		info.Type = "sms_phrase"
	}
	if timeout, ok := s.GetTimeout(); ok {
		info.Timeout = time.Duration(timeout) * time.Second
	}
	if next, ok := s.GetNextType(); ok {
		switch next.(type) {
		case *tg.AuthCodeTypeSMS:
			info.NextType = "sms"
		case *tg.AuthCodeTypeCall:
			info.NextType = "call"
		case *tg.AuthCodeTypeFlashCall:
			info.NextType = "flash_call"
		case *tg.AuthCodeTypeMissedCall:
			info.NextType = "missed_call"
		case *tg.AuthCodeTypeFragmentSMS:
			info.NextType = "fragment_sms"
		default:
			info.NextType = "unknown"
		}
	}
	return info
}
