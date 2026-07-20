package auth

import "testing"

func TestLoginLockout(t *testing.T) {
	s := &Service{logins: map[string]*loginState{}}
	for i := 0; i < loginMaxFails-1; i++ {
		s.loginFailed("alice")
	}
	if err := s.loginAllowed("alice"); err != nil {
		t.Fatalf("under the cap should be allowed: %v", err)
	}
	s.loginFailed("alice") // hits the cap
	if err := s.loginAllowed("alice"); err == nil {
		t.Fatal("at the cap alice should be locked out")
	}
	if err := s.loginAllowed("bob"); err != nil {
		t.Fatalf("other users unaffected: %v", err)
	}
	s.loginReset("alice") // a successful login clears it
	if err := s.loginAllowed("alice"); err != nil {
		t.Fatalf("after reset alice should be allowed: %v", err)
	}
}
