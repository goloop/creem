[![Go Reference](https://img.shields.io/badge/godoc-reference-blue)](https://pkg.go.dev/github.com/goloop/creem) [![License](https://img.shields.io/badge/license-MIT-brightgreen)](https://github.com/goloop/creem/blob/master/LICENSE) [![Stay with Ukraine](https://img.shields.io/static/v1?label=Stay%20with&message=Ukraine%20♥&color=ffD700&labelColor=0057B8&style=flat)](https://u24.gov.ua/)

# creem

`creem` - Go-клієнт для Creem API і перевірка його вебхуків: серверна
частина продажів через Creem як merchant of record - разові продукти,
довільні суми, підписки, місця (seats), зміна плану, повернення й підписані
події. Лише стандартна бібліотека.

Англійська версія: **[README.md](README.md)**.

## Яку проблему розв'язує

Creem розміщує checkout, списує гроші й займається податками; ваш сервер
створює сесії й реагує на події. Пастки ті самі, що з будь-яким
провайдером, плюс дві власні:

- **Сума має приходити з сервера.** Checkout створюється тут, а власна ціна
  пакета кредитів перевіряється до надсилання.
- **Підпис вебхука не містить часу.** Перехоплена доставка проходить
  перевірку завжди, тож id події треба зберігати й перевіряти - пакет робить
  це явним і відхиляє події без id.
- **Формати різняться.** Час приходить то рядками ISO, то мілісекундами;
  посилання - то id, то розгорнутими об'єктами. `Time` і `Expandable` читають
  обидва варіанти, тож обробник не зламається в день, коли поле змінить
  форму.
- **POST, що впав, міг виконатися.** Повернення чи скасування після 5xx не
  повторюються; повторюється лише те, що точно не виконалося.

## Можливості

- Сесії checkout з `request_id`, metadata, заповненим клієнтом, кодами
  знижок і власною ціною для разових продуктів.
- Підписки: отримати, скасувати (одразу чи в кінці періоду), призупинити,
  відновити, перейти на інший продукт, змінити кількість місць.
- Повні повернення; клієнти за id чи email; посилання на платіжний портал;
  транзакції.
- Вебхуки: перевірка підпису HMAC-SHA256 з ротацією секрету; типізовані
  checkout, підписки, повернення й спори.
- Типізована `*APIError`; хост визначається ключем (тестовий чи бойовий).
- Нуль залежностей.

## Встановлення

```bash
go get github.com/goloop/creem
```

## Швидкий старт

```go
c := creem.New(os.Getenv("CREEM_API_KEY")) // тестовий ключ іде на тестовий хост

co, err := c.CreateCheckout(ctx, &creem.CheckoutRequest{
	ProductID:   "prod_credits",
	RequestID:   purchaseID,
	CustomPrice: 1000, // центи, лише для разових продуктів
	Customer:    &creem.CheckoutCustomer{Email: email},
	SuccessURL:  "https://app.example.com/billing?paid=1",
})
// перенаправити на co.CheckoutURL
```

## Моделі продажу

| Модель | Приклад | Як |
|---|---|---|
| **Разово, фіксована ціна** | ліцензія, фіксований пакет | разовий продукт |
| **Разово, сума з сервера** | пакети кредитів, індивідуальні замовлення | `CustomPrice` на одному разовому продукті |
| **Підписка** | $15 на місяць, $60 на рік | продукт-підписка на кожен строк (місяць, 3 чи 6 місяців, рік) |
| **Місця (seats)** | $8 за учасника | `Units` у checkout, згодом `UpdateSubscriptionItems` |
| **Зміна плану** | Personal на Team, місяць на рік | `UpgradeSubscription` з режимом перерахунку |
| **Скасування, пауза, передумав** | скасувати в кінці періоду | `CancelSubscription`, `ResumeSubscription`, `PauseSubscription` |
| **Знижки** | код до запуску | `DiscountCode` у checkout |
| **Повернення й chargeback** | 14 днів на відмову | `RefundTransaction` (лише повне), `refund.created`, `dispute.created` |

Через Creem недоступно: регулярна сума, визначена на сервері (`CustomPrice`
лише для разових продуктів), часткові повернення, рахунки з відтермінуванням
і строки понад рік серед стандартних періодів оплати - для цього є Paddle.
Кредити й ліміти живуть у [goloop/ledger](https://github.com/goloop/ledger).
Довідник показує кожну модель у коді: [Моделі продажу](DOC.UK.md#моделі-продажу).

## Реальний приклад

```go
wh := creem.NewWebhook(os.Getenv("CREEM_WEBHOOK_SECRET"))

http.HandleFunc("POST /webhooks/creem", func(w http.ResponseWriter, r *http.Request) {
	ev, err := wh.ParseRequest(r)
	if err != nil {
		http.Error(w, "bad signature", http.StatusBadRequest)
		return
	}
	// Підпис без часу: збережений id події - захист від відтворення.
	if seen, _ := events.Seen(r.Context(), ev.ID); seen { // ваша таблиця
		w.WriteHeader(http.StatusOK)
		return
	}
	switch ev.Type {
	case creem.EventCheckoutCompleted:
		co, _ := ev.Checkout()
		err = purchases.Paid(r.Context(), co.RequestID, co.Order.Amount, co.Order.Currency)
	case creem.EventSubscriptionPaid, creem.EventSubscriptionCanceled, creem.EventSubscriptionExpired:
		sub, _ := ev.Subscription()
		err = entitlements.Sync(r.Context(), sub)
	case creem.EventRefundCreated:
		ref, _ := ev.Refund()
		err = purchases.Refunded(r.Context(), ref.Checkout.ID, ref.ID)
	}
	if err != nil {
		http.Error(w, "retry", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
})
```

## API коротко

| Виклик | Ендпоінт |
|---|---|
| `CreateCheckout`, `GetCheckout` | `/v1/checkouts` |
| `GetSubscription`, `CancelSubscription`, `PauseSubscription`, `ResumeSubscription` | `/v1/subscriptions[/{id}/cancel,/pause,/resume]` |
| `UpgradeSubscription`, `UpdateSubscriptionItems` | `/v1/subscriptions/{id}[/upgrade]` |
| `RefundTransaction` | `/v1/refunds` |
| `GetCustomer`, `CustomerByEmail`, `CustomerPortalLink` | `/v1/customers[/billing]` |
| `GetTransaction` | `/v1/transactions` |
| `NewWebhook`, `Webhook.ParseRequest`, `Sign` | події вебхуків |

## Документація

- Повний довідник і моделі продажу: [DOC.UK.md](DOC.UK.md) · [DOC.md](DOC.md)
- API пакета: [pkg.go.dev/github.com/goloop/creem](https://pkg.go.dev/github.com/goloop/creem)
- Зміни між версіями: [CHANGELOG.md](CHANGELOG.md)
- Гаманці кредитів поверх: [goloop/ledger](https://github.com/goloop/ledger)

## Участь

Внески вітаються. Перед pull request запустіть `go test -race ./...`,
`go vet ./...` і `gofmt -l .`.

## Ліцензія

`creem` поширюється за ліцензією MIT. Див. [LICENSE](LICENSE).
