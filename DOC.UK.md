# creem - довідник

Повний довідник пакета `creem`: клієнт, checkout, підписки, повернення,
клієнти, транзакції, вебхуки, формати даних Creem, повтори й помилки.

Англійська версія: **[DOC.md](DOC.md)**.

## Зміст

- [Модель](#модель)
- [Створення клієнта](#створення-клієнта)
- [Checkout](#checkout)
- [Підписки](#підписки)
- [Повернення](#повернення)
- [Клієнти, портал, транзакції](#клієнти-портал-транзакції)
- [Моделі продажу](#моделі-продажу)
- [Вебхуки](#вебхуки)
- [Формати: Time і Expandable](#формати-time-і-expandable)
- [Повтори](#повтори)
- [Помилки](#помилки)
- [Межі пакета](#межі-пакета)

## Модель

Creem - merchant of record: він продає власні продукти з каталогу, списує
гроші, займається податками й повідомляє про події через підписані вебхуки.
Сервер створює сесію checkout, відправляє туди клієнта й реагує на події.

Джерело правди про гроші - провайдер; про те, чим клієнт може
користуватися, - ваша база. Вебхуки - дорога між ними: спершу перевірка,
потім дедуплікація за id події.

## Створення клієнта

```go
c := creem.New(os.Getenv("CREEM_API_KEY"))
```

Хост визначається ключем: тестовий ключ (`creem_test_...`) іде на
`https://test-api.creem.io`, будь-який інший - на `https://api.creem.io`.
`WithTest(bool)` і `WithBaseURL(url)` перевизначають це; `Test()` каже, куди
звертається клієнт. Ключ надсилається як `x-api-key`.

| Опція | За замовчуванням |
|---|---|
| `WithTimeout` | 30s (лише для HTTP-клієнта за замовчуванням) |
| `WithHTTPClient` | новий `http.Client` |
| `WithMaxRetries` | 2 |
| `WithHeader` | немає |

## Checkout

```go
co, err := c.CreateCheckout(ctx, &creem.CheckoutRequest{
	ProductID:  "prod_pro_yearly",
	RequestID:  purchaseID,
	Customer:   &creem.CheckoutCustomer{Email: "ann@example.com"},
	SuccessURL: "https://app.example.com/billing?paid=1",
	Metadata:   map[string]any{"purchase": purchaseID},
})
// перенаправити на co.CheckoutURL
```

- `ProductID` обов'язковий: Creem продає лише продукти з каталогу.
- `CustomPrice` (центи за одиницю, від 100 до 99 999 999) перевизначає ціну
  продукту для разових продуктів. Так списують суму, визначену на сервері, -
  пакет кредитів - під одним продуктом.
- `Customer` - це `ID` наявного клієнта або `Email`, не обидва.
- `RequestID` і `Metadata` повертаються в checkout і його подіях: кладіть
  туди id покупки.

`GetCheckout(ctx, id)` читає сесію.

## Підписки

```go
sub, err := c.GetSubscription(ctx, "sub_...")

sub, err = c.CancelSubscription(ctx, id, &creem.CancelRequest{Mode: creem.CancelScheduled})
sub, err = c.CancelSubscription(ctx, id, &creem.CancelRequest{Mode: creem.CancelImmediate})
sub, err = c.ResumeSubscription(ctx, id) // також відкликає заплановане скасування

sub, err = c.PauseSubscription(ctx, id)
sub, err = c.UpgradeSubscription(ctx, id, "prod_team", creem.ProrationChargeImmediately)
```

Заплановане скасування лишає підписку в `SubscriptionScheduledCancel` до
кінця періоду. Порожній `Mode` бере налаштування магазину.
`CurrentPeriodEndDate` - до коли клієнт заплатив.

## Повернення

```go
status, err := c.RefundTransaction(ctx, "tran_...")
// creem.RefundPending, RefundSucceeded, RefundRequiresAction, ...
```

API повертає залишок транзакції повністю; часткового повернення немає. Далі
приходить вебхук `refund.created`. Chargeback приходить як
`dispute.created`; обробляйте його як повернення.

## Клієнти, портал, транзакції

```go
cu, err := c.GetCustomer(ctx, "cust_...")
cu, err = c.CustomerByEmail(ctx, "ann@example.com")

link, err := c.CustomerPortalLink(ctx, cu.ID) // де клієнт керує оплатою

tr, err := c.GetTransaction(ctx, "tran_...")
```

## Моделі продажу

Creem продає **продукти** зі свого каталогу. Продукт - разовий або підписка
з періодом оплати, і checkout продає один продукт у певній кількості
одиниць. Кожна модель нижче - вибір продукту й полів checkout.

### Разова покупка за фіксованою ціною

Ліцензія, фіксований пакет: разовий продукт за ціною з каталогу.

```go
co, err := c.CreateCheckout(ctx, &creem.CheckoutRequest{
	ProductID: "prod_maker_licence",
	RequestID: purchaseID,
	Customer:  &creem.CheckoutCustomer{Email: email},
})
```

На `checkout.completed` видайте право в таблиці прав, за ключем
`co.RequestID`.

### Разова покупка, сума з сервера

Пакети кредитів, замовлення з ціною за формулою: один разовий продукт і сума
в `CustomPrice` (центи за одиницю, від 100 до 99 999 999).

```go
co, err := c.CreateCheckout(ctx, &creem.CheckoutRequest{
	ProductID:   "prod_credits",
	RequestID:   purchaseID,
	CustomPrice: pack.Cents,
})
```

Коли прийде вебхук, перед нарахуванням звірте `co.Order.Amount` і
`co.Order.Currency` з рядком покупки.

### Підписки та строки

Період оплати належить продукту: створіть окремий продукт-підписку на кожен
строк (місяць, три чи шість місяців, рік) і продавайте той, який обрав
клієнт. `CustomPrice` до підписок не застосовується, тож кожен строк має
свою ціну в каталозі.

```go
co, err := c.CreateCheckout(ctx, &creem.CheckoutRequest{
	ProductID: "prod_pro_yearly",
	RequestID: "sub:" + accountID,
	Customer:  &creem.CheckoutCustomer{Email: email},
})
```

Давайте права плану до `CurrentPeriodEndDate` і пересувайте цю дату на
кожному `subscription.paid`. Строку понад рік серед стандартних періодів
Creem немає; уточніть у Creem, перш ніж його пропонувати.

Пробний період налаштовується в продукті; тоді підписка починається як
`SubscriptionTrialing`.

### Місця (seats)

Підписка за місце продається з `Units` і змінюється згодом:

```go
co, err := c.CreateCheckout(ctx, &creem.CheckoutRequest{ProductID: "prod_team_seat", Units: 10})

sub, err := c.UpdateSubscriptionItems(ctx, sub.ID,
	[]creem.SubscriptionItemUpdate{{ID: sub.Items[0].ID, Units: 12}},
	creem.ProrationChargeImmediately)
```

### Зміна плану

Перевести підписку на інший продукт - Personal на Team, місяць на рік:

```go
sub, err := c.UpgradeSubscription(ctx, sub.ID, "prod_team_yearly", creem.ProrationChargeImmediately)
```

| Режим | Дія |
|---|---|
| `ProrationChargeImmediately` | списати пропорційну різницю зараз (за замовчуванням) |
| `ProrationCharge` | додати пропорційну різницю до наступного рахунку |
| `ProrationNone` | перейти без перерахунку |

### Скасування, пауза, «я передумав»

```go
sub, err := c.CancelSubscription(ctx, id, &creem.CancelRequest{Mode: creem.CancelScheduled})
sub, err = c.ResumeSubscription(ctx, id) // відкликає заплановане скасування
sub, err = c.PauseSubscription(ctx, id)
```

`CustomerPortalLink` дає власну сторінку Creem, де клієнт керує підпискою й
карткою.

### Знижки

Передайте код, створений у дашборді Creem: `DiscountCode: "LAUNCH30"`.

### Повернення, відмова, chargeback

```go
status, err := c.RefundTransaction(ctx, transactionID) // лише повне повернення
```

Далі приходить `refund.created`; заберіть те, що куплено за ці гроші, з
ключем - id повернення. Chargeback приходить як `dispute.created`. Часткового
повернення через API немає.

### Чого Creem не робить

- регулярна сума, визначена на сервері (узгоджений план);
- часткові повернення;
- рахунки з відтермінуванням для компаній;
- баланс кредитів чи облік використання - продавайте пакети як разові
  покупки, а баланс тримайте в [goloop/ledger](https://github.com/goloop/ledger).

Для перших трьох запасний варіант - Paddle; `goloop/paddle` їх покриває.

## Вебхуки

```go
wh := creem.NewWebhook(secret)
ev, err := wh.ParseRequest(r)
```

Перевірка:

- Заголовок `creem-signature` - hex HMAC-SHA256 від сирого тіла під секретом
  вебхука. Тіло має бути байтами в тому вигляді, в якому прийшло.
- `WithPreviousSecret` приймає старий секрет під час ротації.
- Порожній секрет нічого не підтверджує, тож неправильно налаштований сервіс
  відхиляє кожну доставку, а не приймає підробки.
- **Підпис не містить часу.** Перехоплена доставка проходить перевірку
  завжди, тож дедуплікація за `ev.ID` - не лише спосіб ігнорувати повтор, а
  й єдиний захист від відтворення. Зберігайте id кожної обробленої події.

Обробка:

- Відповідайте 200, щойно подію збережено; невдалу доставку Creem повторює
  кілька разів протягом наступних годин.
- `ev.CreatedAt` - коли подію створено; використовуйте його чи `UpdatedAt`
  сутності, щоб ігнорувати подію, старшу за вже застосований стан.

Декодування: `ev.Checkout()` для `checkout.completed`, `ev.Subscription()`
для `subscription.*`, `ev.Refund()`, `ev.Dispute()` або `ev.Decode(&v)`.

У завершеному checkout `co.Order` містить списане (`Amount`, `Currency`,
`Type` "onetime" чи "recurring"), `co.RequestID` - ваше посилання,
`co.Subscription` - підписку, яку він почав, якщо є.

`Sign(secret, body)` будує коректний підпис - для тестів вашого обробника.

## Формати: Time і Expandable

Creem надсилає час рядками ISO у більшості сутностей і мілісекундами від
епохи в транзакціях і конвертах вебхуків. `Time` вбудовує `time.Time` і
читає обидва; `null` і відсутнє поле - нульовий час.

Посилання на іншу сутність буває id, а буває розгорнутою сутністю.
`Expandable[T]` читає обидва варіанти:

```go
co.Customer.ID     // заповнений завжди
co.Customer.Object // *Customer, якщо розгорнуто, інакше nil
```

## Повтори

| Невдача | GET | POST |
|---|---|---|
| 429 | повтор після `Retry-After` | повтор після `Retry-After` |
| 5xx | повтор з паузою | повертається |
| мережева помилка | повтор з паузою | повертається |
| інші 4xx | повертається | повертається |

POST, що впав з 5xx, міг виконатися - повернення, скасування, - тож повтор
лишається рішенням того, хто викликає: він може спершу перечитати стан.

## Помилки

```go
var ae *creem.APIError
if errors.As(err, &ae) {
	log.Printf("%d %s: %v (trace %s)", ae.Status, ae.Reason, ae.Messages, ae.TraceID)
}
if errors.Is(err, creem.ErrNotFound) { ... } // будь-який 404
```

`Messages` - список; іноді Creem надсилає один рядок, і він стає списком з
одного елемента. `ErrInvalid` означає запит, відхилений до надсилання.

## Межі пакета

Серверні виклики, потрібні продукту: без керування продуктами, ліцензій,
кодів знижок чи пагінації пошуку. Спільний шлях `do` приймає будь-який
ендпоінт.

Пакет залежить лише від стандартної бібліотеки.
