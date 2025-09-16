package mongo

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/x/mongo/driver"
	"go.uber.org/zap"
)

// SessionState represents the state of a MongoDB session
type SessionState string

const (
	SessionStateActive   SessionState = "active"
	SessionStateInactive SessionState = "inactive"
	SessionStateEnded    SessionState = "ended"
)

// TransactionState represents the state of a transaction within a session
type TransactionState string

const (
	TransactionStateNone       TransactionState = "none"
	TransactionStateStarting   TransactionState = "starting"
	TransactionStateInProgress TransactionState = "in_progress"
	TransactionStateCommitted  TransactionState = "committed"
	TransactionStateAborted    TransactionState = "aborted"
)

// Session represents a MongoDB logical session
type Session struct {
	ID               string
	LsID             []byte
	ClientID         string
	State            SessionState
	CreatedAt        time.Time
	LastActivity     time.Time
	TransactionState TransactionState
	CurrentTxnNumber int64
	PinnedServer     driver.Server
	PinnedConnection driver.Connection
	mutex            sync.RWMutex
}

// SessionManager manages MongoDB sessions and their lifecycle
type SessionManager struct {
	sessions map[string]*Session // Key: base64 encoded lsid
	mutex    sync.RWMutex
	logger   *zap.Logger
	metrics  interface{} // Will be used for metrics later
}

// NewSessionManager creates a new session manager
func NewSessionManager(logger *zap.Logger) *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
		logger:   logger,
	}
}

// GenerateLogicalSessionID generates a new logical session ID
func GenerateLogicalSessionID() ([]byte, error) {
	// Generate 16 random bytes for the logical session ID
	lsid := make([]byte, 16)
	_, err := rand.Read(lsid)
	if err != nil {
		return nil, fmt.Errorf("failed to generate logical session ID: %w", err)
	}
	return lsid, nil
}

// CreateSession creates a new session for a client
func (sm *SessionManager) CreateSession(clientID string) (*Session, error) {
	lsid, err := GenerateLogicalSessionID()
	if err != nil {
		return nil, err
	}

	sessionID := base64.StdEncoding.EncodeToString(lsid)

	session := &Session{
		ID:               sessionID,
		LsID:             lsid,
		ClientID:         clientID,
		State:            SessionStateActive,
		CreatedAt:        time.Now(),
		LastActivity:     time.Now(),
		TransactionState: TransactionStateNone,
		CurrentTxnNumber: 0,
	}

	sm.mutex.Lock()
	sm.sessions[sessionID] = session
	sm.mutex.Unlock()

	sm.logger.Debug("Created new session",
		zap.String("session_id", sessionID),
		zap.String("client_id", clientID))

	return session, nil
}

// GetSession retrieves a session by its logical session ID
func (sm *SessionManager) GetSession(lsid []byte) (*Session, bool) {
	sessionID := base64.StdEncoding.EncodeToString(lsid)

	sm.mutex.RLock()
	session, exists := sm.sessions[sessionID]
	sm.mutex.RUnlock()

	if exists && session.State != SessionStateEnded {
		// Update last activity
		session.mutex.Lock()
		session.LastActivity = time.Now()
		session.mutex.Unlock()
		return session, true
	}

	return nil, false
}

// StartTransaction starts a transaction for a session
func (sm *SessionManager) StartTransaction(session *Session, server driver.Server, connection driver.Connection, txnNumber int64) error {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	if session.State != SessionStateActive {
		return fmt.Errorf("cannot start transaction on inactive session")
	}

	if session.TransactionState != TransactionStateNone {
		return fmt.Errorf("transaction already in progress")
	}

	// Use the transaction number from the command, don't increment it
	session.CurrentTxnNumber = txnNumber
	session.TransactionState = TransactionStateInProgress
	session.PinnedServer = server
	session.PinnedConnection = connection
	session.LastActivity = time.Now()

	sm.logger.Debug("Started transaction",
		zap.String("session_id", session.ID),
		zap.Int64("txn_number", session.CurrentTxnNumber))

	return nil
}

// CommitTransaction commits a transaction
func (sm *SessionManager) CommitTransaction(session *Session) error {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	if session.TransactionState != TransactionStateInProgress {
		sm.logger.Error("Cannot commit transaction - no transaction in progress",
			zap.String("session_id", session.ID),
			zap.String("current_state", string(session.TransactionState)),
			zap.Int64("txn_number", session.CurrentTxnNumber))
		return fmt.Errorf("no transaction in progress to commit")
	}

	session.TransactionState = TransactionStateCommitted
	// Don't unpin the connection yet - let the actual commit command be sent first
	session.LastActivity = time.Now()

	sm.logger.Debug("Committed transaction",
		zap.String("session_id", session.ID),
		zap.Int64("txn_number", session.CurrentTxnNumber),
		zap.String("state", string(session.TransactionState)))

	return nil
}

// UnpinTransaction unpins the server and connection after transaction completion
func (sm *SessionManager) UnpinTransaction(session *Session) {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	session.PinnedServer = nil
	session.PinnedConnection = nil
	// Don't reset transaction state here - let it be reset after connection is closed
	// session.TransactionState = TransactionStateNone
	// session.CurrentTxnNumber = 0

	sm.logger.Debug("Unpinned transaction resources",
		zap.String("session_id", session.ID))
}

// ResetTransactionState resets the transaction state after connection is closed
func (sm *SessionManager) ResetTransactionState(session *Session) {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	session.TransactionState = TransactionStateNone
	session.CurrentTxnNumber = 0
	session.PinnedServer = nil
	session.PinnedConnection = nil

	sm.logger.Debug("Reset transaction state",
		zap.String("session_id", session.ID))
}

// AbortTransaction aborts a transaction
func (sm *SessionManager) AbortTransaction(session *Session) error {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	if session.TransactionState != TransactionStateInProgress {
		return fmt.Errorf("no transaction in progress to abort")
	}

	session.TransactionState = TransactionStateAborted
	// Don't unpin the connection yet - let the actual abort command be sent first
	session.LastActivity = time.Now()

	sm.logger.Debug("Aborted transaction",
		zap.String("session_id", session.ID),
		zap.Int64("txn_number", session.CurrentTxnNumber))

	return nil
}

// EndSession ends a session and removes it from the session manager
func (sm *SessionManager) EndSession(session *Session) error {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	if session.State == SessionStateEnded {
		return nil // Already ended
	}

	// If there's an active transaction, abort it
	if session.TransactionState == TransactionStateInProgress {
		session.TransactionState = TransactionStateAborted
		session.PinnedServer = nil
		session.PinnedConnection = nil
	}

	session.State = SessionStateEnded
	session.LastActivity = time.Now()

	sm.logger.Debug("Ended session",
		zap.String("session_id", session.ID),
		zap.String("client_id", session.ClientID))

	// Remove the session from the session manager
	sm.mutex.Lock()
	delete(sm.sessions, session.ID)
	sm.mutex.Unlock()

	sm.logger.Debug("Removed session from session manager",
		zap.String("session_id", session.ID),
		zap.String("client_id", session.ClientID))

	return nil
}

// CleanupInactiveSessions removes sessions that have been inactive for too long
func (sm *SessionManager) CleanupInactiveSessions(maxInactiveTime time.Duration) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	now := time.Now()
	var sessionsToRemove []string

	for sessionID, session := range sm.sessions {
		session.mutex.RLock()
		lastActivity := session.LastActivity
		sessionState := session.State
		session.mutex.RUnlock()

		// Remove sessions that are ended or have been inactive for too long
		if sessionState == SessionStateEnded || now.Sub(lastActivity) > maxInactiveTime {
			sessionsToRemove = append(sessionsToRemove, sessionID)
		}
	}

	// Remove inactive sessions
	for _, sessionID := range sessionsToRemove {
		delete(sm.sessions, sessionID)
		sm.logger.Debug("Cleaned up inactive session",
			zap.String("session_id", sessionID))
	}

	if len(sessionsToRemove) > 0 {
		sm.logger.Info("Cleaned up inactive sessions",
			zap.Int("removed_count", len(sessionsToRemove)),
			zap.Int("remaining_count", len(sm.sessions)))
	}
}

// GetSessionCount returns the number of active sessions
func (sm *SessionManager) GetSessionCount() int {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	return len(sm.sessions)
}

// GetPinnedServer returns the pinned server for a session if it has an active transaction
func (sm *SessionManager) GetPinnedServer(session *Session) (driver.Server, bool) {
	session.mutex.RLock()
	defer session.mutex.RUnlock()

	if session.TransactionState == TransactionStateInProgress && session.PinnedServer != nil {
		return session.PinnedServer, true
	}

	return nil, false
}

// GetPinnedConnection returns the pinned connection for a session if it has an active transaction
func (sm *SessionManager) GetPinnedConnection(session *Session) (driver.Connection, bool) {
	session.mutex.RLock()
	defer session.mutex.RUnlock()

	// Return pinned connection if transaction is in progress OR if it's committed/aborted
	// This ensures commit/abort commands use the same connection
	if (session.TransactionState == TransactionStateInProgress ||
		session.TransactionState == TransactionStateCommitted ||
		session.TransactionState == TransactionStateAborted) && session.PinnedConnection != nil {
		return session.PinnedConnection, true
	}

	return nil, false
}

// IsTransactionActive checks if a session has an active transaction
func (sm *SessionManager) IsTransactionActive(session *Session) bool {
	session.mutex.RLock()
	defer session.mutex.RUnlock()

	// Only consider transaction active if it's actually in progress
	// Committed/aborted transactions should allow connection cleanup
	return session.TransactionState == TransactionStateInProgress
}

// CleanupExpiredSessions removes expired sessions
func (sm *SessionManager) CleanupExpiredSessions(maxAge time.Duration) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	now := time.Now()
	for sessionID, session := range sm.sessions {
		session.mutex.RLock()
		age := now.Sub(session.LastActivity)
		session.mutex.RUnlock()

		if age > maxAge {
			sm.logger.Debug("Cleaning up expired session",
				zap.String("session_id", sessionID),
				zap.Duration("age", age))

			delete(sm.sessions, sessionID)
		}
	}
}

// GetActiveTransactionCount returns the number of sessions with active transactions
func (sm *SessionManager) GetActiveTransactionCount() int {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()

	count := 0
	for _, session := range sm.sessions {
		if session.TransactionState == TransactionStateInProgress {
			count++
		}
	}

	return count
}
