package admin

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "unitool/internal/db/generated"
)

type Service struct {
	Q *db.Queries
}

type PackageInput struct {
	Code          string
	Name          string
	PriceRub      int32
	Currency      string
	AttemptsText  int32
	AttemptsImage int32
	AttemptsVideo int32
	IsActive      bool
}

type UserCard struct {
	User           db.User
	Status         string
	TotalGenerates int64
	LastPackage    string
}

type Stats struct {
	TotalUsers int64
	Active     int64
	Banned     int64
	New24h     int64
	New7d      int64
	TotalGens  int64
	TopUsers   []db.TopUsersByGenerationCountRow
}

func NewService(q *db.Queries) *Service {
	return &Service{Q: q}
}

func (s *Service) ListPackages(ctx context.Context) ([]db.Package, error) {
	return s.Q.ListPackages(ctx)
}

func (s *Service) CreatePackage(ctx context.Context, in PackageInput) (db.Package, error) {
	params, err := validatePackageInput(in)
	if err != nil {
		return db.Package{}, err
	}
	return s.Q.CreatePackage(ctx, params)
}

func (s *Service) UpdatePackage(ctx context.Context, id int64, in PackageInput) (db.Package, error) {
	params, err := validatePackageInput(in)
	if err != nil {
		return db.Package{}, err
	}
	return s.Q.UpdatePackage(ctx, db.UpdatePackageParams{
		ID:           id,
		Code:         params.Code,
		Title:        params.Title,
		PriceRub:     params.PriceRub,
		Currency:     params.Currency,
		TextCredits:  params.TextCredits,
		ImageCredits: params.ImageCredits,
		VideoCredits: params.VideoCredits,
		IsActive:     params.IsActive,
	})
}

func (s *Service) SetPackageActive(ctx context.Context, id int64, active bool) error {
	affected, err := s.Q.SetPackageActive(ctx, db.SetPackageActiveParams{ID: id, IsActive: active})
	if err != nil {
		return err
	}
	if affected == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Service) DeletePackage(ctx context.Context, id int64) error {
	affected, err := s.Q.DeletePackage(ctx, id)
	if err != nil {
		return err
	}
	if affected == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Service) FindUserByTGID(ctx context.Context, tgID int64) (UserCard, error) {
	u, err := s.Q.GetUserByTGID(ctx, tgID)
	if err != nil {
		return UserCard{}, err
	}
	return s.buildUserCard(ctx, u)
}

func (s *Service) FindUsersByUsername(ctx context.Context, term string, limit int32) ([]db.User, error) {
	term = strings.TrimSpace(term)
	if term == "" {
		return nil, errors.New("username is empty")
	}
	if limit <= 0 {
		limit = 10
	}
	return s.Q.SearchUsersByUsername(ctx, db.SearchUsersByUsernameParams{
		Username: pgtype.Text{String: "%" + term + "%", Valid: true},
		Limit:    limit,
	})
}

func (s *Service) BuildUserCardByID(ctx context.Context, id int64) (UserCard, error) {
	u, err := s.Q.GetUserByID(ctx, id)
	if err != nil {
		return UserCard{}, err
	}
	return s.buildUserCard(ctx, u)
}

func (s *Service) SetBanByTGID(ctx context.Context, adminTGID, targetTGID int64, banned bool, reason string) error {
	target, err := s.Q.GetUserByTGID(ctx, targetTGID)
	if err != nil {
		return err
	}
	affected, err := s.Q.SetUserBanStatus(ctx, db.SetUserBanStatusParams{
		ID:           target.ID,
		IsBanned:     banned,
		BannedReason: pgtype.Text{String: reason, Valid: reason != ""},
	})
	if err != nil {
		return err
	}
	if affected == 0 {
		return pgx.ErrNoRows
	}
	action := "unban"
	if banned {
		action = "ban"
	}
	log.Printf("admin action=%s admin_tg_id=%d target_tg_id=%d reason=%q", action, adminTGID, targetTGID, reason)
	return nil
}

func (s *Service) IsUserBanned(ctx context.Context, tgID int64) (bool, error) {
	u, err := s.Q.GetUserByTGID(ctx, tgID)
	if err != nil {
		return false, err
	}
	return u.IsBanned, nil
}

func (s *Service) GetStats(ctx context.Context, topN int32) (Stats, error) {
	if topN <= 0 {
		topN = 10
	}
	total, err := s.Q.CountUsers(ctx)
	if err != nil {
		return Stats{}, err
	}
	active, err := s.Q.CountActiveUsers(ctx)
	if err != nil {
		return Stats{}, err
	}
	banned, err := s.Q.CountBannedUsers(ctx)
	if err != nil {
		return Stats{}, err
	}
	new24h, err := s.Q.CountNewUsersSince(ctx, pgtype.Timestamptz{
		Time:  time.Now().UTC().Add(-24 * time.Hour),
		Valid: true,
	})
	if err != nil {
		return Stats{}, err
	}
	new7d, err := s.Q.CountNewUsersSince(ctx, pgtype.Timestamptz{
		Time:  time.Now().UTC().Add(-7 * 24 * time.Hour),
		Valid: true,
	})
	if err != nil {
		return Stats{}, err
	}
	totalGens, err := s.Q.CountGenerationRequests(ctx)
	if err != nil {
		return Stats{}, err
	}
	top, err := s.Q.TopUsersByGenerationCount(ctx, topN)
	if err != nil {
		return Stats{}, err
	}
	return Stats{
		TotalUsers: total,
		Active:     active,
		Banned:     banned,
		New24h:     new24h,
		New7d:      new7d,
		TotalGens:  totalGens,
		TopUsers:   top,
	}, nil
}

func (s *Service) buildUserCard(ctx context.Context, u db.User) (UserCard, error) {
	total, err := s.Q.CountGenerationRequestsByUser(ctx, u.ID)
	if err != nil {
		return UserCard{}, err
	}
	last := "нет данных"
	if paid, err := s.Q.GetLastPaidPackageByUser(ctx, u.ID); err == nil {
		last = fmt.Sprintf("%s (%s, %d %s)", paid.Title, paid.Code, paid.PriceRub, paid.Currency)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return UserCard{}, err
	}
	status := "active"
	if u.IsBanned {
		status = "banned"
	}
	return UserCard{
		User:           u,
		Status:         status,
		TotalGenerates: total,
		LastPackage:    last,
	}, nil
}

func validatePackageInput(in PackageInput) (db.CreatePackageParams, error) {
	code := strings.TrimSpace(strings.ToLower(in.Code))
	name := strings.TrimSpace(in.Name)
	curr := strings.ToUpper(strings.TrimSpace(in.Currency))
	if curr == "" {
		curr = "RUB"
	}
	if code == "" || name == "" {
		return db.CreatePackageParams{}, errors.New("code and name are required")
	}
	if in.PriceRub < 0 || in.AttemptsText < 0 || in.AttemptsImage < 0 || in.AttemptsVideo < 0 {
		return db.CreatePackageParams{}, errors.New("numeric values must be >= 0")
	}
	if len(curr) != 3 {
		return db.CreatePackageParams{}, errors.New("currency must be 3-letter code")
	}
	if curr != "RUB" {
		return db.CreatePackageParams{}, errors.New("only RUB currency is supported in current payment flow")
	}
	return db.CreatePackageParams{
		Code:         code,
		Title:        name,
		PriceRub:     in.PriceRub,
		Currency:     curr,
		TextCredits:  in.AttemptsText,
		ImageCredits: in.AttemptsImage,
		VideoCredits: in.AttemptsVideo,
		IsActive:     in.IsActive,
	}, nil
}
