package telegram

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func MainReplyKeyboard() tgbotapi.ReplyKeyboardMarkup {
	row1 := tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("Создать картинку"),
		tgbotapi.NewKeyboardButton("Создать видео"),
	)
	row2 := tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("Мой профиль"),
		tgbotapi.NewKeyboardButton("Купить"),
	)
	kb := tgbotapi.NewReplyKeyboard(row1, row2)
	kb.ResizeKeyboard = true
	kb.OneTimeKeyboard = false
	return kb
}

func ModelsInlineKeyboard(mode string, selected string) tgbotapi.InlineKeyboardMarkup {
	// Mode switch row
	tabs := tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Фото", "mode:image"),
		tgbotapi.NewInlineKeyboardButtonData("Видео", "mode:video"),
	)

	// Model rows
	models := ModelUIList(mode)

	// build buttons in rows of up to 2-3
	rows := [][]tgbotapi.InlineKeyboardButton{tabs}
	row := []tgbotapi.InlineKeyboardButton{}
	for i, m := range models {
		label := m
		if m == selected {
			label = "✅ " + m
		}
		data := "model:" + mode + ":" + m
		row = append(row, tgbotapi.NewInlineKeyboardButtonData(label, data))
		if len(row) == 3 || i == len(models)-1 {
			rows = append(rows, row)
			row = []tgbotapi.InlineKeyboardButton{}
		}
	}
	return tgbotapi.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func PackagesInlineKeyboard(codes []string) tgbotapi.InlineKeyboardMarkup {
	rows := [][]tgbotapi.InlineKeyboardButton{}
	row := []tgbotapi.InlineKeyboardButton{}
	for i, code := range codes {
		label := code
		row = append(row, tgbotapi.NewInlineKeyboardButtonData(label, "buy:"+code))
		if len(row) == 3 || i == len(codes)-1 {
			rows = append(rows, row)
			row = []tgbotapi.InlineKeyboardButton{}
		}
	}
	return tgbotapi.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func AdminMainInlineKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Packages", "admin:packages"),
			tgbotapi.NewInlineKeyboardButtonData("Users", "admin:users"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Stats", "admin:stats"),
			tgbotapi.NewInlineKeyboardButtonData("Ban / Unban", "admin:ban"),
		),
	)
}

func AdminPackagesInlineKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("List", "admin:packages:list"),
			tgbotapi.NewInlineKeyboardButtonData("Add", "admin:packages:add"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Edit", "admin:packages:edit"),
			tgbotapi.NewInlineKeyboardButtonData("Delete", "admin:packages:delete"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Activate", "admin:packages:activate"),
			tgbotapi.NewInlineKeyboardButtonData("Deactivate", "admin:packages:deactivate"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Back", "admin:menu"),
		),
	)
}

func AdminUsersInlineKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Find by user_id", "admin:users:find_id"),
			tgbotapi.NewInlineKeyboardButtonData("Find by username", "admin:users:find_username"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Back", "admin:menu"),
		),
	)
}

func AdminBanInlineKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Ban", "admin:ban:set"),
			tgbotapi.NewInlineKeyboardButtonData("Unban", "admin:ban:unset"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Back", "admin:menu"),
		),
	)
}
