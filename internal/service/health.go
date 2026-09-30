package service

import "context"

// Healthy checks the database is reachable.
func (s *Service) Healthy(ctx context.Context) error {
	_, err := s.Store.Ping(ctx)
	return err
}
