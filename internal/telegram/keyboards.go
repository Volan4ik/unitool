package telegram

import (
    tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Static model lists
var (
    textModels   = []string{"GPT-5", "Claude 4.5", "Gemini 2.5 Pro", "Grok 4"}
    searchModels = []string{"Perplexity", "GPT-5", "Claude 4.5", "Gemini 2.5 Pro", "Grok 4"}
    imageModels  = []string{"Flux", "Midjourney", "Ideogram"}
    videoModels  = []string{"Sora", "Kling", "Hailuo"}
)

func MainReplyKeyboard() tgbotapi.ReplyKeyboardMarkup {
    row1 := tgbotapi.NewKeyboardButtonRow(
        tgbotapi.NewKeyboardButton("Сгенерировать текст"),
        tgbotapi.NewKeyboardButton("Интернет-поиск"),
        tgbotapi.NewKeyboardButton("Создать картинку"),
        tgbotapi.NewKeyboardButton("Создать видео"),
    )
    row2 := tgbotapi.NewKeyboardButtonRow(
        tgbotapi.NewKeyboardButton("Создать песню"),
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
        tgbotapi.NewInlineKeyboardButtonData("Текст", "mode:text"),
        tgbotapi.NewInlineKeyboardButtonData("Поиск", "mode:search"),
        tgbotapi.NewInlineKeyboardButtonData("Фото", "mode:image"),
        tgbotapi.NewInlineKeyboardButtonData("Видео", "mode:video"),
    )

    // Model rows
    var models []string
    switch mode {
    case "text":
        models = textModels
    case "search":
        models = searchModels
    case "image":
        models = imageModels
    case "video":
        models = videoModels
    default:
        models = textModels
    }

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
