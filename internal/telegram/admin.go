package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"unitool/internal/admin"
	db "unitool/internal/db/generated"
)

const (
	adminActionPkgAdd              = "pkg_add"
	adminActionPkgEdit             = "pkg_edit"
	adminActionPkgDelete           = "pkg_delete"
	adminActionPkgActivate         = "pkg_activate"
	adminActionPkgDeactivate       = "pkg_deactivate"
	adminActionUserFindID          = "user_find_id"
	adminActionUserFindName        = "user_find_name"
	adminActionBan                 = "ban_set"
	adminActionUnban               = "ban_unset"
	adminActionBroadcast           = "broadcast_send"
	adminUserSearchLimit     int32 = 10
	adminTopUsersLimit       int32 = 10
)

func (r *Router) handleAdminCommand(ctx context.Context, m *tgbotapi.Message) error {
	if m.From == nil || !r.isAdminTGID(m.From.ID) {
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Доступ запрещён."))
		return nil
	}
	r.clearAdminFlow(m.From.ID)
	msg := tgbotapi.NewMessage(m.Chat.ID, "Admin panel")
	msg.ReplyMarkup = AdminMainInlineKeyboard()
	r.Bot.API.Send(msg)
	return nil
}

func (r *Router) handleAdminCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, parts []string) error {
	if cq == nil {
		return nil
	}
	chatID, ok := callbackChatID(cq)
	if !ok {
		return nil
	}
	if cq.From == nil || !r.isAdminTGID(cq.From.ID) {
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Доступ запрещён."))
		return nil
	}
	if r.Admin == nil {
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Admin service не настроен."))
		return nil
	}
	if len(parts) < 2 {
		msg := tgbotapi.NewMessage(chatID, "Admin panel")
		msg.ReplyMarkup = AdminMainInlineKeyboard()
		r.Bot.API.Send(msg)
		return nil
	}

	switch parts[1] {
	case "menu":
		r.clearAdminFlow(cq.From.ID)
		msg := tgbotapi.NewMessage(chatID, "Admin panel")
		msg.ReplyMarkup = AdminMainInlineKeyboard()
		r.Bot.API.Send(msg)
	case "packages":
		if len(parts) == 2 {
			msg := tgbotapi.NewMessage(chatID, "Packages")
			msg.ReplyMarkup = AdminPackagesInlineKeyboard()
			r.Bot.API.Send(msg)
			return nil
		}
		return r.handleAdminPackagesAction(ctx, cq.From.ID, chatID, parts[2:])
	case "users":
		if len(parts) == 2 {
			msg := tgbotapi.NewMessage(chatID, "Users")
			msg.ReplyMarkup = AdminUsersInlineKeyboard()
			r.Bot.API.Send(msg)
			return nil
		}
		return r.handleAdminUsersAction(cq.From.ID, chatID, parts[2:])
	case "stats":
		return r.sendAdminStats(ctx, chatID)
	case "ban":
		if len(parts) == 2 {
			msg := tgbotapi.NewMessage(chatID, "Ban / Unban")
			msg.ReplyMarkup = AdminBanInlineKeyboard()
			r.Bot.API.Send(msg)
			return nil
		}
		return r.handleAdminBanAction(cq.From.ID, chatID, parts[2:])
	case "broadcast":
		if r.Notifier == nil {
			r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Сервис рассылок не настроен."))
			return nil
		}
		r.setAdminFlow(cq.From.ID, adminActionBroadcast)
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Введите текст рассылки для всех пользователей."))
		return nil
	default:
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Неизвестное действие админки."))
	}
	return nil
}

func (r *Router) handleAdminTextInput(ctx context.Context, m *tgbotapi.Message, adminTGID int64, txt string) (bool, error) {
	if !r.isAdminTGID(adminTGID) {
		return false, nil
	}
	if r.Admin == nil {
		return false, nil
	}
	state, ok := r.getAdminFlow(adminTGID)
	if !ok {
		return false, nil
	}
	switch state.Action {
	case adminActionPkgAdd:
		in, err := parsePackageInput(txt)
		if err != nil {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Формат: code|name|price|currency|attempts_image|attempts_video|is_active"))
			return true, nil
		}
		p, err := r.Admin.CreatePackage(ctx, in)
		if err != nil {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Ошибка создания пакета: %v", err)))
			return true, err
		}
		r.clearAdminFlow(adminTGID)
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Пакет создан:\n"+formatPackage(p)))
		return true, nil
	case adminActionPkgEdit:
		id, in, err := parsePackageEditInput(txt)
		if err != nil {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Формат: id|code|name|price|currency|attempts_image|attempts_video|is_active"))
			return true, nil
		}
		p, err := r.Admin.UpdatePackage(ctx, id, in)
		if err != nil {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Ошибка редактирования пакета: %v", err)))
			return true, err
		}
		r.clearAdminFlow(adminTGID)
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Пакет обновлён:\n"+formatPackage(p)))
		return true, nil
	case adminActionPkgDelete:
		id, err := strconv.ParseInt(strings.TrimSpace(txt), 10, 64)
		if err != nil {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Введите числовой package id."))
			return true, nil
		}
		if err := r.Admin.DeletePackage(ctx, id); err != nil {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Ошибка удаления пакета: %v", err)))
			return true, err
		}
		r.clearAdminFlow(adminTGID)
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Пакет удалён."))
		return true, nil
	case adminActionPkgActivate, adminActionPkgDeactivate:
		id, err := strconv.ParseInt(strings.TrimSpace(txt), 10, 64)
		if err != nil {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Введите числовой package id."))
			return true, nil
		}
		active := state.Action == adminActionPkgActivate
		if err := r.Admin.SetPackageActive(ctx, id, active); err != nil {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Ошибка обновления статуса пакета: %v", err)))
			return true, err
		}
		r.clearAdminFlow(adminTGID)
		status := "деактивирован"
		if active {
			status = "активирован"
		}
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Пакет "+status+"."))
		return true, nil
	case adminActionUserFindID:
		tgID, err := strconv.ParseInt(strings.TrimSpace(txt), 10, 64)
		if err != nil {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Введите Telegram user_id числом."))
			return true, nil
		}
		card, err := r.Admin.FindUserByTGID(ctx, tgID)
		if err != nil {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Пользователь не найден: %v", err)))
			return true, err
		}
		r.clearAdminFlow(adminTGID)
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, formatUserCard(card)))
		return true, nil
	case adminActionUserFindName:
		list, err := r.Admin.FindUsersByUsername(ctx, txt, adminUserSearchLimit)
		if err != nil {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Ошибка поиска: %v", err)))
			return true, err
		}
		r.clearAdminFlow(adminTGID)
		if len(list) == 0 {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Пользователи не найдены."))
			return true, nil
		}
		lines := make([]string, 0, len(list))
		for _, u := range list {
			status := "active"
			if u.IsBanned {
				status = "banned"
			}
			lines = append(lines, fmt.Sprintf("id=%d tg_id=%d username=%s status=%s", u.ID, u.TgID, textOrDash(u.Username.String, u.Username.Valid), status))
		}
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, strings.Join(lines, "\n")))
		return true, nil
	case adminActionBan:
		tgID, reason, err := parseBanInput(txt)
		if err != nil {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Формат: tg_user_id|reason"))
			return true, nil
		}
		if err := r.Admin.SetBanByTGID(ctx, adminTGID, tgID, true, reason); err != nil {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Ошибка бана: %v", err)))
			return true, err
		}
		r.clearAdminFlow(adminTGID)
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Пользователь забанен."))
		return true, nil
	case adminActionUnban:
		tgID, err := strconv.ParseInt(strings.TrimSpace(txt), 10, 64)
		if err != nil {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Формат: tg_user_id"))
			return true, nil
		}
		if err := r.Admin.SetBanByTGID(ctx, adminTGID, tgID, false, ""); err != nil {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Ошибка разбана: %v", err)))
			return true, err
		}
		r.clearAdminFlow(adminTGID)
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Пользователь разбанен."))
		return true, nil
	case adminActionBroadcast:
		if r.Notifier == nil {
			r.clearAdminFlow(adminTGID)
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Сервис рассылок не настроен."))
			return true, nil
		}
		body := strings.TrimSpace(txt)
		if body == "" {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Текст рассылки пустой. Введите текст."))
			return true, nil
		}
		campaignID, err := r.Notifier.EnqueueBroadcast(ctx, adminTGID, body)
		if err != nil {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Ошибка запуска рассылки: %v", err)))
			return true, err
		}
		r.clearAdminFlow(adminTGID)
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Рассылка запущена. campaign_id=%d", campaignID)))
		return true, nil
	default:
		r.clearAdminFlow(adminTGID)
		return false, nil
	}
}

func (r *Router) handleAdminPackagesAction(ctx context.Context, adminTGID, chatID int64, parts []string) error {
	if len(parts) == 0 {
		msg := tgbotapi.NewMessage(chatID, "Packages")
		msg.ReplyMarkup = AdminPackagesInlineKeyboard()
		r.Bot.API.Send(msg)
		return nil
	}
	switch parts[0] {
	case "list":
		pkgs, err := r.Admin.ListPackages(ctx)
		if err != nil {
			r.Bot.API.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Ошибка списка пакетов: %v", err)))
			return err
		}
		if len(pkgs) == 0 {
			r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Пакеты отсутствуют."))
			return nil
		}
		lines := make([]string, 0, len(pkgs))
		for _, p := range pkgs {
			lines = append(lines, formatPackage(p))
		}
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, strings.Join(lines, "\n\n")))
	case "add":
		r.setAdminFlow(adminTGID, adminActionPkgAdd)
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Введите пакет:\ncode|name|price|currency|attempts_image|attempts_video|is_active"))
	case "edit":
		r.setAdminFlow(adminTGID, adminActionPkgEdit)
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Введите пакет:\nid|code|name|price|currency|attempts_image|attempts_video|is_active"))
	case "delete":
		r.setAdminFlow(adminTGID, adminActionPkgDelete)
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Введите package id для удаления"))
	case "activate":
		r.setAdminFlow(adminTGID, adminActionPkgActivate)
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Введите package id для активации"))
	case "deactivate":
		r.setAdminFlow(adminTGID, adminActionPkgDeactivate)
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Введите package id для деактивации"))
	default:
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Неизвестное действие packages."))
	}
	return nil
}

func (r *Router) handleAdminUsersAction(adminTGID, chatID int64, parts []string) error {
	if len(parts) == 0 {
		msg := tgbotapi.NewMessage(chatID, "Users")
		msg.ReplyMarkup = AdminUsersInlineKeyboard()
		r.Bot.API.Send(msg)
		return nil
	}
	switch parts[0] {
	case "find_id":
		r.setAdminFlow(adminTGID, adminActionUserFindID)
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Введите Telegram user_id"))
	case "find_username":
		r.setAdminFlow(adminTGID, adminActionUserFindName)
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Введите username или его часть"))
	default:
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Неизвестное действие users."))
	}
	return nil
}

func (r *Router) handleAdminBanAction(adminTGID, chatID int64, parts []string) error {
	if len(parts) == 0 {
		msg := tgbotapi.NewMessage(chatID, "Ban / Unban")
		msg.ReplyMarkup = AdminBanInlineKeyboard()
		r.Bot.API.Send(msg)
		return nil
	}
	switch parts[0] {
	case "set":
		r.setAdminFlow(adminTGID, adminActionBan)
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Введите:\ntg_user_id|reason"))
	case "unset":
		r.setAdminFlow(adminTGID, adminActionUnban)
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Введите tg_user_id"))
	default:
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Неизвестное действие ban/unban."))
	}
	return nil
}

func (r *Router) sendAdminStats(ctx context.Context, chatID int64) error {
	st, err := r.Admin.GetStats(ctx, adminTopUsersLimit)
	if err != nil {
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Ошибка статистики: %v", err)))
		return err
	}
	lines := []string{
		fmt.Sprintf("Users total: %d", st.TotalUsers),
		fmt.Sprintf("Users active: %d", st.Active),
		fmt.Sprintf("Users banned: %d", st.Banned),
		fmt.Sprintf("New users 24h: %d", st.New24h),
		fmt.Sprintf("New users 7d: %d", st.New7d),
		fmt.Sprintf("Generations total: %d", st.TotalGens),
	}
	if len(st.TopUsers) > 0 {
		lines = append(lines, "Top users by generations:")
		for i, u := range st.TopUsers {
			lines = append(lines, fmt.Sprintf("%d) tg_id=%d username=%s gens=%d", i+1, u.TgID, textOrDash(u.Username.String, u.Username.Valid), u.GenCount))
		}
	}
	if len(st.BySource) > 0 {
		lines = append(lines, "Users by source_tag:")
		for _, item := range st.BySource {
			lines = append(lines, fmt.Sprintf("• %s: %d", item.SourceTag, item.UsersCount))
		}
	}
	r.Bot.API.Send(tgbotapi.NewMessage(chatID, strings.Join(lines, "\n")))
	return nil
}

func (r *Router) setAdminFlow(adminTGID int64, action string) {
	r.adminFlowMu.Lock()
	defer r.adminFlowMu.Unlock()
	r.adminFlow[adminTGID] = adminFlowState{Action: action}
}

func (r *Router) clearAdminFlow(adminTGID int64) {
	r.adminFlowMu.Lock()
	defer r.adminFlowMu.Unlock()
	delete(r.adminFlow, adminTGID)
}

func (r *Router) getAdminFlow(adminTGID int64) (adminFlowState, bool) {
	r.adminFlowMu.Lock()
	defer r.adminFlowMu.Unlock()
	v, ok := r.adminFlow[adminTGID]
	return v, ok
}

func parsePackageEditInput(raw string) (int64, admin.PackageInput, error) {
	parts := splitInput(raw, 8)
	if len(parts) != 8 {
		return 0, admin.PackageInput{}, fmt.Errorf("bad input")
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, admin.PackageInput{}, err
	}
	in, err := parsePackageInput(strings.Join(parts[1:], "|"))
	if err != nil {
		return 0, admin.PackageInput{}, err
	}
	return id, in, nil
}

func parsePackageInput(raw string) (admin.PackageInput, error) {
	parts := splitInput(raw, 7)
	if len(parts) != 7 {
		return admin.PackageInput{}, fmt.Errorf("bad input")
	}
	price, err := strconv.ParseInt(parts[2], 10, 32)
	if err != nil {
		return admin.PackageInput{}, err
	}
	img, err := strconv.ParseInt(parts[4], 10, 32)
	if err != nil {
		return admin.PackageInput{}, err
	}
	vid, err := strconv.ParseInt(parts[5], 10, 32)
	if err != nil {
		return admin.PackageInput{}, err
	}
	active, err := parseBool(parts[6])
	if err != nil {
		return admin.PackageInput{}, err
	}
	return admin.PackageInput{
		Code:          parts[0],
		Name:          parts[1],
		PriceRub:      int32(price),
		Currency:      parts[3],
		AttemptsText:  0,
		AttemptsImage: int32(img),
		AttemptsVideo: int32(vid),
		IsActive:      active,
	}, nil
}

func parseBanInput(raw string) (int64, string, error) {
	parts := splitInput(raw, 2)
	if len(parts) != 2 {
		return 0, "", fmt.Errorf("bad input")
	}
	tgID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, "", err
	}
	reason := strings.TrimSpace(parts[1])
	if reason == "" {
		return 0, "", fmt.Errorf("reason required")
	}
	return tgID, reason, nil
}

func parseBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true, nil
	case "0", "false", "no", "n", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid bool %q", raw)
	}
}

func splitInput(raw string, max int) []string {
	chunks := strings.SplitN(raw, "|", max)
	out := make([]string, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, strings.TrimSpace(c))
	}
	return out
}

func formatPackage(p db.Package) string {
	return fmt.Sprintf(
		"id=%d code=%s name=%s price=%d %s image=%d video=%d active=%t created=%s updated=%s",
		p.ID,
		p.Code,
		p.Title,
		p.PriceRub,
		p.Currency,
		p.ImageCredits,
		p.VideoCredits,
		p.IsActive,
		formatTS(p.CreatedAt.Time, p.CreatedAt.Valid),
		formatTS(p.UpdatedAt.Time, p.UpdatedAt.Valid),
	)
}

func formatUserCard(card admin.UserCard) string {
	u := card.User
	lines := []string{
		fmt.Sprintf("user_id=%d", u.TgID),
		fmt.Sprintf("db_id=%d", u.ID),
		fmt.Sprintf("username=%s", textOrDash(u.Username.String, u.Username.Valid)),
		fmt.Sprintf("status=%s", card.Status),
		fmt.Sprintf("registered_at=%s", formatTS(u.CreatedAt.Time, u.CreatedAt.Valid)),
		fmt.Sprintf("balance: image=%d video=%d", u.ImageBalance, u.VideoBalance),
		fmt.Sprintf("last_package=%s", card.LastPackage),
		fmt.Sprintf("total_generations=%d", card.TotalGenerates),
	}
	return strings.Join(lines, "\n")
}

func textOrDash(s string, ok bool) string {
	if ok && s != "" {
		return s
	}
	return "-"
}

func formatTS(t time.Time, ok bool) string {
	if !ok {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}
