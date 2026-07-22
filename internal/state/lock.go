package state

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/danlavee/Conductor/internal/platform"
)

var leaseSequence atomic.Uint64

func (c *Client) acquireWrite(resource, agent string) (Lock, error) {
	return c.acquireWriteWithGuardOwner(resource, agent, "")
}

func (c *Client) acquireWriteWhileHoldingTransactionGuard(resource, agent string) (Lock, error) {
	return c.acquireWriteWithGuardOwner(resource, agent, agent)
}

func (c *Client) acquireWriteWithGuardOwner(resource, agent, guardOwner string) (Lock, error) {
	path := c.writeLockPath(resource)
	guard := c.writeGuardPath(resource)
	for attempts := 0; attempts < 3; attempts++ {
		releaseMutex, err := c.acquireStateMutex(c.resourceMutexPath(resource))
		if err != nil {
			return Lock{}, err
		}
		lock, err := c.newLockFor(agent, os.Getpid())
		if err != nil {
			_ = releaseMutex()
			return Lock{}, err
		}
		err = os.Mkdir(guard, 0o700)
		if err == nil {
			if err := writeJSONAtomic(path, lock); err != nil {
				_ = removeEventually(guard)
				_ = releaseMutex()
				return Lock{}, err
			}
			readers, err := c.readerCount(resource)
			if err != nil || readers > 0 {
				_ = removeEventually(path)
				_ = removeEventually(guard)
				_ = releaseMutex()
				if err != nil {
					return Lock{}, err
				}
				return Lock{}, &ProtocolError{Code: "LOCKED", Text: "resource has active readers"}
			}
			if err := releaseMutex(); err != nil {
				return Lock{}, err
			}
			return lock, nil
		}
		if errors.Is(err, os.ErrPermission) {
			_ = releaseMutex()
			return Lock{}, &ProtocolError{Code: "LOCKED", Text: "resource lock is changing ownership"}
		}
		if !errors.Is(err, os.ErrExist) {
			_ = releaseMutex()
			return Lock{}, err
		}
		var existing Lock
		if err := readJSON(path, &existing); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				_ = releaseMutex()
				return Lock{}, err
			}
			info, statErr := os.Stat(guard)
			if statErr == nil && time.Since(info.ModTime()) >= c.LockTimeout {
				_ = removeEventually(path)
				if removeErr := removeEventually(guard); removeErr == nil {
					_ = releaseMutex()
					continue
				}
			}
			_ = releaseMutex()
			return Lock{}, &ProtocolError{Code: "LOCKED", Text: "resource lock is being initialized"}
		}
		expires := existing.Timestamp.Add(time.Duration(existing.TimeoutSec) * time.Second)
		if time.Now().UTC().Before(expires) {
			_ = releaseMutex()
			return Lock{}, &ProtocolError{Code: "LOCKED", Agent: existing.Agent, Text: "resource is locked"}
		}
		if lockOwnerAlive(existing) {
			_ = releaseMutex()
			return Lock{}, &ProtocolError{Code: "TIMEOUT", Agent: existing.Agent, Text: "lock expired but the owner process is still alive"}
		}
		claim, err := c.newLockFor(existing.Agent, os.Getpid())
		if err != nil {
			_ = releaseMutex()
			return Lock{}, err
		}
		if err := writeJSONAtomic(path, claim); err != nil {
			_ = releaseMutex()
			return Lock{}, err
		}
		if err := releaseMutex(); err != nil {
			return Lock{}, err
		}
		var recoveryErr error
		if existing.Agent == guardOwner {
			recoveryErr = c.recoverExpiredWhileHoldingTransactionGuard(resource, existing)
		} else {
			recoveryErr = c.recoverExpired(resource, existing)
		}
		if recoveryErr != nil {
			c.restoreExpiredClaim(resource, claim, existing)
			return Lock{}, &ProtocolError{Code: "TIMEOUT", Agent: existing.Agent, Text: recoveryErr.Error()}
		}
	}
	return Lock{}, &ProtocolError{Code: "TIMEOUT", Text: "could not take over expired lock"}
}

func (c *Client) recoverExpired(resource string, lock Lock) error {
	releaseGuard, err := c.acquireTransactionGuard(lock.Agent)
	if err != nil {
		return err
	}
	defer releaseGuard()
	return c.recoverExpiredWhileHoldingTransactionGuard(resource, lock)
}

func (c *Client) recoverExpiredWhileHoldingTransactionGuard(resource string, lock Lock) error {
	var txn Transaction
	err := readJSON(c.transactionPath(lock.Agent), &txn)
	if err == nil && txn.Resource == resource {
		if len(txn.Messages) > 0 {
			if _, err := c.commitTransaction(txn); err != nil {
				return fmt.Errorf("expired transaction could not be flushed: %w", err)
			}
			return nil
		}
		if removeErr := removeEventually(c.transactionPath(lock.Agent)); removeErr != nil {
			return removeErr
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return c.releaseWrite(resource, lock.Agent)
}

func (c *Client) releaseWrite(resource, agent string) error {
	releaseMutex, err := c.acquireStateMutex(c.resourceMutexPath(resource))
	if err != nil {
		return err
	}
	defer releaseMutex()
	path := c.writeLockPath(resource)
	var lock Lock
	if err := readJSON(path, &lock); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if lock.Agent != agent {
		return &ProtocolError{Code: "LOCKED", Agent: lock.Agent, Text: "cannot release another agent's lock"}
	}
	if err := removeEventually(path); err != nil {
		return err
	}
	if err := removeEventually(c.writeGuardPath(resource)); err != nil {
		return err
	}
	return nil
}

func (c *Client) renewWrite(resource, agent string) error {
	releaseMutex, err := c.acquireStateMutex(c.resourceMutexPath(resource))
	if err != nil {
		return err
	}
	defer releaseMutex()
	if _, err := os.Stat(c.writeGuardPath(resource)); err != nil {
		return &ProtocolError{Code: "NO_LOCK", Text: "transaction resource lock is missing"}
	}
	path := c.writeLockPath(resource)
	var lock Lock
	if err := readJSON(path, &lock); err != nil {
		return err
	}
	if lock.Agent != agent {
		return &ProtocolError{Code: "LOCKED", Agent: lock.Agent, Text: "transaction resource is owned by another agent"}
	}
	lock, err = c.newLock(agent)
	if err != nil {
		return err
	}
	return writeJSONAtomic(path, lock)
}

func (c *Client) acquireRead(resource string) (func() error, error) {
	if err := validResource(resource); err != nil {
		return nil, err
	}
	for {
		releaseMutex, err := c.acquireStateMutex(c.resourceMutexPath(resource))
		if err != nil {
			return nil, err
		}
		guardPath := c.writeGuardPath(resource)
		if _, err := os.Stat(guardPath); err == nil {
			var lock Lock
			if err := readJSON(c.writeLockPath(resource), &lock); err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					_ = releaseMutex()
					return nil, err
				}
				info, statErr := os.Stat(guardPath)
				if statErr == nil && time.Since(info.ModTime()) >= c.LockTimeout {
					if removeErr := removeEventually(guardPath); removeErr == nil {
						_ = releaseMutex()
						continue
					}
				}
				_ = releaseMutex()
				return nil, &ProtocolError{Code: "LOCKED", Text: "resource lock is being initialized"}
			}
			expires := lock.Timestamp.Add(time.Duration(lock.TimeoutSec) * time.Second)
			if time.Now().UTC().Before(expires) {
				_ = releaseMutex()
				return nil, &ProtocolError{Code: "LOCKED", Agent: lock.Agent, Text: "resource is write-locked"}
			}
			if lockOwnerAlive(lock) {
				_ = releaseMutex()
				return nil, &ProtocolError{Code: "TIMEOUT", Agent: lock.Agent, Text: "lock expired but the owner process is still alive"}
			}
			claim, err := c.newLockFor(lock.Agent, os.Getpid())
			if err != nil {
				_ = releaseMutex()
				return nil, err
			}
			if err := writeJSONAtomic(c.writeLockPath(resource), claim); err != nil {
				_ = releaseMutex()
				return nil, err
			}
			if err := releaseMutex(); err != nil {
				return nil, err
			}
			if err := c.recoverExpired(resource, lock); err != nil {
				c.restoreExpiredClaim(resource, claim, lock)
				return nil, &ProtocolError{Code: "TIMEOUT", Agent: lock.Agent, Text: err.Error()}
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			_ = releaseMutex()
			return nil, err
		}
		readerDir := c.readerLockDir(resource)
		if err := os.MkdirAll(readerDir, 0o700); err != nil {
			_ = releaseMutex()
			return nil, err
		}
		marker := filepath.Join(readerDir, strconv.Itoa(os.Getpid())+"-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".lock")
		reader, err := c.newLockFor(c.Agent, os.Getpid())
		if err != nil {
			_ = releaseMutex()
			return nil, err
		}
		if err := writeJSONAtomic(marker, reader); err != nil {
			_ = releaseMutex()
			return nil, err
		}
		if err := releaseMutex(); err != nil {
			_ = removeEventually(marker)
			return nil, err
		}
		return func() error { return removeEventually(marker) }, nil
	}
}

func (c *Client) readerCount(resource string) (int, error) {
	entries, err := os.ReadDir(c.readerLockDir(resource))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".lock" {
			continue
		}
		path := filepath.Join(c.readerLockDir(resource), entry.Name())
		var reader Lock
		if err := readJSON(path, &reader); err != nil {
			count++
			continue
		}
		expired := !time.Now().UTC().Before(reader.Timestamp.Add(time.Duration(reader.TimeoutSec) * time.Second))
		if expired && !lockOwnerAlive(reader) {
			if err := removeEventually(path); err == nil {
				continue
			}
		}
		count++
	}
	return count, nil
}

func (c *Client) writeLockPath(resource string) string {
	return filepath.Join(c.Home, "locks", encodeResource(resource)+".lock")
}

func (c *Client) writeGuardPath(resource string) string {
	return filepath.Join(c.Home, "locks", encodeResource(resource)+".guard")
}

func (c *Client) readerLockDir(resource string) string {
	return filepath.Join(c.Home, "locks", encodeResource(resource)+".readers")
}

func (c *Client) resourceMutexPath(resource string) string {
	return filepath.Join(c.Home, "locks", encodeResource(resource)+".mutex")
}

func (c *Client) newLock(agent string) (Lock, error) {
	return c.newLockFor(agent, c.ownerProcessID())
}

func (c *Client) newLockFor(agent string, pid int) (Lock, error) {
	start, ok := platform.ProcessStartToken(pid)
	if !ok {
		return Lock{}, fmt.Errorf("cannot identify process instance %d", pid)
	}
	timeoutSec := int(math.Ceil(c.LockTimeout.Seconds()))
	if timeoutSec < 1 {
		timeoutSec = 1
	}
	return Lock{PID: pid, ProcessStart: start, LeaseID: leaseSequence.Add(1), Agent: agent, Timestamp: time.Now().UTC(), TimeoutSec: timeoutSec}, nil
}

func lockOwnerAlive(lock Lock) bool {
	if current, ok := platform.ProcessStartToken(lock.PID); ok {
		return current == lock.ProcessStart && platform.ProcessAlive(lock.PID)
	}
	return platform.ProcessAlive(lock.PID)
}

func (c *Client) restoreExpiredClaim(resource string, claim, expired Lock) {
	release, err := c.acquireStateMutex(c.resourceMutexPath(resource))
	if err != nil {
		return
	}
	defer release()
	var current Lock
	if readJSON(c.writeLockPath(resource), &current) == nil && current.PID == claim.PID && current.ProcessStart == claim.ProcessStart && current.LeaseID == claim.LeaseID && current.Timestamp.Equal(claim.Timestamp) {
		_ = writeJSONAtomic(c.writeLockPath(resource), expired)
	}
}

func encodeResource(resource string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(resource))
}

func removeEventually(path string) error {
	var err error
	for attempt := 0; attempt < 100; attempt++ {
		err = os.Remove(path)
		if err == nil {
			return platform.SyncParent(path)
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if !errors.Is(err, os.ErrPermission) {
			return err
		}
		time.Sleep(2 * time.Millisecond)
	}
	return err
}
