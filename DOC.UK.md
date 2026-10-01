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
- [Звірка](#звірка)
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
| `WithHTTPClient` | новий `http.Client`; nil його лишає |
| `WithMaxRetries` | 2, не більше `MaxRetries` (10) |
| `WithHeader` | немає |

Що клієнт відхиляє за будь-яких опцій:

- **Редиректи.** API не має причин їх надсилати, а `x-api-key` - не той
  заголовок, який `net/http` прибирає дорогою на інший хост: перехід за
  редиректом віддав би ключ. 3xx повертається як `*APIError`. Клієнт,
  переданий у `WithHTTPClient`, для цього копіюється й не змінюється.
- **Звичайний HTTP**, окрім loopback-хоста (тестовий сервер), і порожній
  ключ. Обидва випадки - `ErrInvalid` ще до надсилання.
- **Id, що міг би змінити шлях.** Id може містити літери, цифри, `_` і `-`,
  не довше 128 байт; усе інше - `ErrInvalid`.

Ключ ніколи не потрапляє у вивід: `String`, `GoString` і `LogValue`
показують лише хост. `Webhook` так само приховує свої секрети.

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

API повертає залишок транзакції повністю; часткового повернення через API
немає. Далі приходить вебхук `refund.created`. Повернення з дашборду Creem
може бути частковим, тож забирайте пропорційно `Refund.RefundAmount`, а не
всю покупку. Chargeback приходить як `dispute.created`; обробляйте його як
повернення на `Dispute.Amount`.

## Клієнти, портал, транзакції

```go
cu, err := c.GetCustomer(ctx, "cust_...")
cu, err = c.CustomerByEmail(ctx, "ann@example.com")

link, err := c.CustomerPortalLink(ctx, cu.ID) // де клієнт керує оплатою

tr, err := c.GetTransaction(ctx, "tran_...")
page, err := c.ListTransactions(ctx, &creem.ListTransactionsRequest{CustomerID: cu.ID})
```

`CustomerByEmail` і `CustomerPortalLink` мають стояти за адресою, яку
користувач підтвердив, і за id клієнта, який ваша база пов'язує з поточним
акаунтом: портал показує замовлення й підписки того, хто купував з цією
адресою.

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
ключем - id повернення, пропорційно `RefundAmount` (повернення з дашборду
може бути частковим). Chargeback приходить як `dispute.created`, ключ - id
спору. Часткового повернення через API немає.

### Чого Creem не робить

- регулярна сума, визначена на сервері (узгоджений план);
- часткові повернення;
- рахунки з відтермінуванням для компаній;
- баланс кредитів чи облік використання - продавайте пакети як разові
  покупки, а баланс тримайте в [goloop/ledger](https://github.com/goloop/ledger).

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
- **Підпис не містить часу**, тож перехоплена доставка проходила б перевірку
  завжди. Але `created_at` лежить у підписаному тілі, і `Parse` відхиляє
  подію, далі за `DefaultMaxAge` (48 годин) від локального годинника, з
  `ErrEventExpired`, що відповідає й `ErrSignature`. Creem припиняє повтори
  доставки протягом доби, тож справжня подія ніколи не буває такою старою.
  `WithMaxAge` змінює вікно; нуль вимикає перевірку. Подія без `created_at`
  відхиляється, поки перевірка ввімкнена.
- **Зберігайте id кожної обробленої події** і пропускайте вже бачену: у
  межах вікна саме id зупиняє відтворення.

Обробка:

- Відповідайте 200, щойно подію застосовано. Невдалу доставку Creem
  повторює через 30 секунд, 5 хвилин, 30 хвилин і 6 годин, і не пізніше ніж
  через 24 години.
- Позначайте подію виконаною лише після застосування: позначка наперед губить
  подію, коли застосування падає. Після останнього повтору див.
  [Звірка](#звірка).
- У `checkout.completed` поля `co.Order` може не бути; перевіряйте його.
- Звіряйте `Mode` сутності зі своїм середовищем: тестова подія, що дійшла до
  бойового обробника, відхиляється, а не нараховується.
- `RefundTransaction`, що повернув `RefundPending` чи `RefundRequiresAction`, -
  це ще не повернення: гроші рухаються, коли про це каже `refund.created`.
- `ev.CreatedAt` - коли подію створено; використовуйте його чи `UpdatedAt`
  сутності, щоб ігнорувати подію, старшу за вже застосований стан.

Декодування: `ev.Checkout()` для `checkout.completed`, `ev.Subscription()`
для `subscription.*`, `ev.Refund()`, `ev.Dispute()` або `ev.Decode(&v)`.

У завершеному checkout `co.Order` містить списане (`Amount`, `Currency`,
`Type` "onetime" чи "recurring"), `co.RequestID` - ваше посилання,
`co.Subscription` - підписку, яку він почав, якщо є.

`Sign(secret, body)` будує коректний підпис - для тестів вашого обробника.

## Звірка

Вебхук може загубитися назавжди: помилка в коді, збій, довший за добу,
неправильно змінений секрет. Оплата, чий вебхук не прийшов, - це клієнт, який
заплатив і нічого не отримав. Періодична задача проходить транзакції й
застосовує кожну оплачену так, як це зробив би вебхук:

```go
req := &creem.ListTransactionsRequest{Page: 1, PageSize: 50}
for {
	page, err := c.ListTransactions(ctx, req)
	if err != nil {
		return err
	}
	for _, tr := range page.Items {
		if tr.Status == creem.TransactionPaid {
			if err := purchases.PaidTransaction(ctx, &tr); err != nil { // той самий код, що й у вебхуку
				return err
			}
		}
	}
	if page.NextPage == 0 {
		break
	}
	req.Page = page.NextPage
}
```

Ключі ledger - id провайдера - роблять транзакцію, яку вебхук уже
застосував, тут холостою. Фільтруйте за `CustomerID`, `ProductID` чи
`OrderID`, щоб прохід був коротким. Сторінка, чия наступна не рухається
вперед, чи з елементом без id - це `ErrResponse`, а не кінець. Сторінки
нумеровані, тож нові транзакції під час проходу можуть зсунути елементи між
сторінками: робіть проходи з перекриттям і зберігайте контрольну точку лише
після проходу до останньої сторінки. Повернені й оскаржені транзакції
(`TransactionRefunded`, `TransactionPartialRefund`, `TransactionChargedBack`)
теж належать до проходу.

## Формати: Time і Expandable

Creem надсилає час рядками ISO у більшості сутностей і мілісекундами від
епохи в транзакціях і конвертах вебхуків. `Time` вбудовує `time.Time` і
читає обидва; `null` і відсутнє поле - нульовий час. Число, менше за 10^11,
читається як секунди: як мілісекунди це була б дата в 1973 році.

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
Пауза - 0.5s, 1s, 2s ... з розкидом, не більше 8s; `Retry-After` - у секундах
чи як HTTP-дата - обмежено 60 секундами ще до перетворення, тож величезне
значення не переповниться. Запит, який неможливо побудувати, завершується
одразу.

## Помилки

```go
var ae *creem.APIError
if errors.As(err, &ae) {
	log.Printf("%d %s: %v (trace %s)", ae.Status, ae.Reason, ae.Messages, ae.TraceID)
}
if errors.Is(err, creem.ErrNotFound) { ... } // будь-який 404
```

`Messages` - список; іноді Creem надсилає один рядок, і він стає списком з
одного елемента. `ErrInvalid` означає запит, відхилений до надсилання:
бракує поля, id поза дозвільним списком, порожній ключ, базовий URL зі
звичайним HTTP.

`ErrResponse` означає успішну відповідь, у якій немає того, що чекає
виклик: порожнє тіло, сутність без id чи з іншим id, ніж просили, невідомий
статус повернення, номер сторінки, що не рухається вперед. Таке приходить від зламаного проксі,
тестової підміни чи зміни API, і на це ніколи не можна діяти як на
результат.

`APIError.Raw` містить тіло відповіді як є. Пишіть у журнал статус, код і id
запиту, а не `Raw`: тіло може містити дані клієнта.

## Межі пакета

Серверні виклики, потрібні продукту: без керування продуктами, ліцензій,
кодів знижок чи пагінації пошуку. Спільний шлях `do` приймає будь-який
ендпоінт.

Пакет залежить лише від стандартної бібліотеки.
