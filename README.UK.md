[![deps.dev](https://img.shields.io/badge/deps.dev-insights-4c8dbc)](https://deps.dev/go/github.com%2Fgoloop%2Fcreem) [![Go Reference](https://img.shields.io/badge/godoc-reference-blue)](https://pkg.go.dev/github.com/goloop/creem) [![License](https://img.shields.io/badge/license-MIT-brightgreen)](https://github.com/goloop/creem/blob/v1/LICENSE) [![Stay with Ukraine](https://img.shields.io/static/v1?label=Stay%20with&message=Ukraine%20♥&color=ffD700&labelColor=0057B8&style=flat)](https://u24.gov.ua/)

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
- **Підпис вебхука не містить часу.** Перехоплена доставка проходила б
  перевірку завжди. Пакет перевіряє `created_at` події - він у підписаному
  тілі - і відхиляє подію, старішу за два дні, а id події однаково треба
  зберігати й перевіряти.
- **Формати різняться.** Час приходить то рядками ISO, то мілісекундами;
  посилання - то id, то розгорнутими об'єктами. `Time` і `Expandable` читають
  обидва варіанти, тож обробник не зламається в день, коли поле змінить
  форму.
- **POST, що впав, міг виконатися.** Повернення чи скасування після 5xx не
  повторюються; повторюється лише те, що точно не виконалося.
- **Загублений вебхук - загублені гроші.** Creem припиняє повтори через добу;
  `ListTransactions` дає періодичній задачі знайти те, що не прийшло.

## Можливості

- Сесії checkout з `request_id`, metadata, заповненим клієнтом, кодами
  знижок і власною ціною для разових продуктів.
- Підписки: отримати, скасувати (одразу чи в кінці періоду), призупинити,
  відновити, перейти на інший продукт, змінити кількість місць.
- Повні повернення; клієнти за id чи email; посилання на платіжний портал;
  транзакції - поодинці чи посторінково для звірки.
- Вебхуки: перевірка підпису HMAC-SHA256 з ротацією секрету і перевірка
  віку; типізовані checkout, підписки, повернення й спори.
- Типізована `*APIError`; хост визначається ключем (тестовий чи бойовий);
  редиректи не виконуються, id перевіряються за дозвільним списком, ключі не
  друкуються.
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
і строки понад рік серед стандартних періодів оплати.
Кредити й ліміти живуть у [goloop/ledger](https://github.com/goloop/ledger).
Довідник показує кожну модель у коді: [Моделі продажу](DOC.UK.md#моделі-продажу).

## Реальний приклад

```go
wh := creem.NewWebhook(os.Getenv("CREEM_WEBHOOK_SECRET"))

http.HandleFunc("POST /webhooks/creem", func(w http.ResponseWriter, r *http.Request) {
	ev, err := wh.ParseRequest(r) // також відхиляє подію, старішу за DefaultMaxAge
	if err != nil {
		http.Error(w, "bad signature", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	// // Таблиця подій - скорочення, а не захист: apply ідемпотентний сам
	// (записи з ключами), а подію позначено виконаною лише після застосування,
	// тож невдачу тут Creem повторить, і вона не загубиться.
	done, err := events.Done(ctx, ev.ID) // ваша таблиця
	if err != nil {
		http.Error(w, "retry", http.StatusInternalServerError)
		return
	}
	if done {
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := apply(ctx, ev); err != nil {
		http.Error(w, "retry", http.StatusInternalServerError) // Creem надішле ще раз
		return
	}
	if err := events.MarkDone(ctx, ev.ID); err != nil {
		http.Error(w, "retry", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
})

func apply(ctx context.Context, ev *creem.Event) error {
	switch ev.Type {
	case creem.EventCheckoutCompleted:
		co, err := ev.Checkout()
		if err != nil {
			return err
		}
		if co.Order == nil {
			return fmt.Errorf("checkout %s completed without an order", co.ID)
		}
		// // Звірити суму й валюту з покупкою, потім нарахувати з ключем - id транзакції.
		return purchases.Paid(ctx, co.RequestID, co.Order.Transaction, co.Order.AmountPaid, co.Order.Currency)

	case creem.EventSubscriptionPaid, creem.EventSubscriptionCanceled, creem.EventSubscriptionExpired:
		sub, err := ev.Subscription()
		if err != nil {
			return err
		}
		return entitlements.Sync(ctx, sub, ev.CreatedAt.Time) // ігнорує подію, старішу за стан

	case creem.EventRefundCreated:
		ref, err := ev.Refund()
		if err != nil {
			return err
		}
		return purchases.Refunded(ctx, ref.Transaction.ID, ref.ID, ref.RefundAmount) // // може бути частковим (повернення з дашборду): забрати пропорційно, ключ ref.ID

	case creem.EventDisputeCreated:
		d, err := ev.Dispute()
		if err != nil {
			return err
		}
		return purchases.Refunded(ctx, d.Transaction.ID, d.ID, d.Amount)
	}
	return nil
}
```

## API коротко

| Виклик | Ендпоінт |
|---|---|
| `CreateCheckout`, `GetCheckout` | `/v1/checkouts` |
| `GetSubscription`, `CancelSubscription`, `PauseSubscription`, `ResumeSubscription` | `/v1/subscriptions[/{id}/cancel,/pause,/resume]` |
| `UpgradeSubscription`, `UpdateSubscriptionItems` | `/v1/subscriptions/{id}[/upgrade]` |
| `RefundTransaction` | `/v1/refunds` |
| `GetCustomer`, `CustomerByEmail`, `CustomerPortalLink` | `/v1/customers[/billing]` |
| `GetTransaction`, `ListTransactions` | `/v1/transactions[/search]` |
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

## Реліз v1.0.0

Поточний API оголошено стабільним без змін алгоритмів. Реліз v1 зберігає поточний import path і публічний API. Майбутні несумісні зміни потребуватимуть нової головної версії.
