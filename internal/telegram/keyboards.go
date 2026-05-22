package telegram

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func MainReplyKeyboard() tgbotapi.ReplyKeyboardMarkup {
	row1 := tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("Фото"),
		tgbotapi.NewKeyboardButton("Видео"),
	)
	row2 := tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("Профиль"),
		tgbotapi.NewKeyboardButton("Купить"),
	)
	kb := tgbotapi.NewReplyKeyboard(row1, row2)
	kb.ResizeKeyboard = true
	kb.OneTimeKeyboard = false
	kb.InputFieldPlaceholder = "Напиши промпт"
	return kb
}

func ProfileInlineKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Пополнить баланс", "buy:menu"),
			tgbotapi.NewInlineKeyboardButtonURL("Написать в поддержку", "https://t.me/rusdev77"),
		),
	)
}

func InsufficientBalanceInlineKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Пополнить баланс", "buy:menu"),
			tgbotapi.NewInlineKeyboardButtonData("Мой профиль", "profile:show"),
		),
	)
}

func HelpInlineKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("Написать в поддержку", "https://t.me/rusdev77"),
		),
	)
}

func WelcomeInlineKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("СГЕНЕРИРОВАТЬ ТРЕНДОВОЕ ФОТО", "start:trend_photo"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Потратить бесплатные генерации", "start:mode"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Сразу хочу платный тариф", "buy:menu"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Расскажите про функционал", "support:help"),
		),
	)
}

func ModeInlineKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Фото", "mode:image"),
			tgbotapi.NewInlineKeyboardButtonData("Видео", "mode:video"),
		),
	)
}

func ModelsInlineKeyboard(mode string, selected string) tgbotapi.InlineKeyboardMarkup {
	// Mode switch row
	tabs := ModeInlineKeyboard().InlineKeyboard[0]

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

type PackageButton struct {
	Code  string
	Label string
}

func PackagesInlineKeyboard(items []PackageButton) tgbotapi.InlineKeyboardMarkup {
	rows := [][]tgbotapi.InlineKeyboardButton{}
	for _, item := range items {
		label := item.Label
		if label == "" {
			label = item.Code
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, "buy:"+item.Code),
		))
	}
	return tgbotapi.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func PaymentMethodInlineKeyboard(code string, enableSBP bool) tgbotapi.InlineKeyboardMarkup {
	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💳 Оплатить картой", "buy:tg:"+code),
		),
	}
	if enableSBP {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⚡ Оплатить через СБП", "buy:sbp:"+code),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Назад к тарифам", "buy:menu"),
	))
	return tgbotapi.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func AdminMainInlineKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Тарифы", "admin:packages"),
			tgbotapi.NewInlineKeyboardButtonData("Пользователи", "admin:users"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Статистика", "admin:stats"),
			tgbotapi.NewInlineKeyboardButtonData("Бан / разбан", "admin:ban"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Рассылка", "admin:broadcast"),
		),
	)
}

func AdminPackagesInlineKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Список", "admin:packages:list"),
			tgbotapi.NewInlineKeyboardButtonData("Добавить", "admin:packages:add"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Изменить", "admin:packages:edit"),
			tgbotapi.NewInlineKeyboardButtonData("Удалить", "admin:packages:delete"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Включить", "admin:packages:activate"),
			tgbotapi.NewInlineKeyboardButtonData("Выключить", "admin:packages:deactivate"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Назад", "admin:menu"),
		),
	)
}

func AdminUsersInlineKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Найти по Telegram ID", "admin:users:find_id"),
			tgbotapi.NewInlineKeyboardButtonData("Найти по username", "admin:users:find_username"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Скачать CSV", "admin:users:export_csv"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Назад", "admin:menu"),
		),
	)
}

func AdminBanInlineKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Забанить", "admin:ban:set"),
			tgbotapi.NewInlineKeyboardButtonData("Разбанить", "admin:ban:unset"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Назад", "admin:menu"),
		),
	)
}
