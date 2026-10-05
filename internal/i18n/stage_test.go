package i18n

import "testing"

func TestStage(t *testing.T) {
	cases := map[string]string{
		"Downloading 45.2%":         "Скачивание",
		"Downloading":               "Скачивание",
		"Downloading image":         "Скачивание изображения",
		"Starting download (q720)":  "Начинаю загрузку",
		"Finalizing file":           "Подготовка файла",
		"Trying alternate download": "Пробую другой способ",
		"Uploading to Telegram":     "Отправка в Telegram",
		"Queued (retry)":            "В очереди (повтор)",
		"something custom":          "something custom",
	}
	for in, want := range cases {
		if got := Stage(RU, in); got != want {
			t.Errorf("Stage(ru, %q) = %q, want %q", in, got, want)
		}
	}
	if Status(FA, "done") != "انجام شد" || Status(EN, "weird") != "weird" {
		t.Error("Status translation")
	}
}
