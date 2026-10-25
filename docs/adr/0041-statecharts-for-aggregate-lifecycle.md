# 41. Statecharts для lifecycle агрегатов

Date: 2026-10-26

## Status

Accepted

Дополняет [21. Pure FSM Transitions](0021-pure-fsm-transitions.md): все её правила (чистота,
таблица как данные, типизированные ошибки, строгий `AlreadyInState`, граница с Temporal)
сохраняются. Связан с [22. Specification Pattern](0022-specification-pattern-for-domain-policies.md)
и [24. Ubiquitous Language](0024-ubiquitous-language-for-domain-modeling.md). Подробные правила —
[FSM: Statecharts](../fsm.md#statecharts-иерархия-состояний).

## Context

Плоские transition tables всех четырёх агрегатов (`Order`, `RestaurantOrder`, `Delivery`,
`Payment`) повторяют одно правило для группы состояний:

- `RestaurantOrder`: `Cancel` записан пять раз, `ReportDelay` — три, `ReportFailure` — два;
- `Delivery`: `Fail` из `Assigned` и `PickedUp`, `Cancel` из `Waiting` и `Assigned`;
- `Order`: `Cancel` и `RecordFact` из `placed` и `confirmed`;
- `Payment`: `Fail` из `pending` и `authorized`.

Группы состояний при этом живут вне таблицы и дублируются руками: `TERMINAL_STATES`,
`State::WITH_ACTIVE_ASSIGNMENT`, `State.IsFinal()`, списки `$in` в MongoDB-адаптере kitchen
board. Добавление state требует найти все эти места; таблица перестаёт быть единственным
источником знания о lifecycle, а диаграммы остаются плоскими.

Statecharts (Harel, 1987; семантика W3C SCXML) решают это иерархией состояний: переход,
объявленный на составном состоянии, действует во всех вложенных. Кроме иерархии statecharts
предлагают параллельные регионы, history, entry/exit actions, delayed и eventless transitions.
Часть этих механизмов противоречит ADR 21 (effectful callbacks, чтение времени), поэтому
нужно зафиксировать, какое подмножество принимается.

## Decision

Lifecycle агрегата описывается **чистым statechart**: transition table ADR 21, дополненной
иерархией состояний. Формула перехода не меняется:

```text
leaf state + trigger + domain inputs = next leaf state | typed domain error
```

### Иерархия

- Состояния делятся на **leaf** (атомарные) и **compound** (составные). Иерархия — данные:
  у каждого узла явно задан родитель либо отметка верхнего уровня.
- Aggregate всегда находится ровно в одном leaf state. Persistence, Protobuf, integration
  events и метрики используют только leaf state; compound states не сохраняются и не
  передаются наружу.
- Compound states — термины Ubiquitous Language контекста (например `IN_KITCHEN`), а не
  технические группы. Их имена фиксируются рядом с таблицей и в документации контекста.
- Переход может быть объявлен на leaf или compound state. Целевое состояние перехода — всегда
  leaf state либо **targetless** (`stay`): aggregate остаётся в текущем leaf и меняет только
  данные (`ReportDelay`, `RecordFact`).

### Разрешение перехода

Для пары `(leaf, trigger)` engine проверяет строки от leaf вверх по предкам; побеждает
ближайшая (innermost-first, как в SCXML). Строка вложенного состояния переопределяет строку
предка для того же trigger, в том числе другим guard или другим исходом. На одном узле пара
`(node, trigger)` встречается не больше одного раза.

Строгие повторы (`AlreadyInState`) объявляются только на leaf states. Если ни на leaf, ни на
предках нет строки, исход — `InvalidTransition`, как и раньше. Строка на корне иерархии
запрещена: «любой trigger из любого состояния» — неявное поведение, а не модель.

### Принадлежность

Правила вида «заказ в работе кухни», «delivery с активным Assignment», «terminal state»
выражаются принадлежностью к compound state: `isIn(state, IN_KITCHEN)` для loaded
aggregate и `leavesOf(ACTIVE_PREPARATION)` для фильтров хранилища. Ручные списки состояний
в domain, application и adapters заменяются этими функциями.

### Что не принимается

- **Entry/exit actions** и любые callbacks — запрещены по ADR 21.
- **Delayed (`after`) и eventless (`always`) transitions** — время и ожидания принадлежат
  application clock и Temporal; FSM не планирует переходы сама.
- **History states** — восстановление идёт из persisted leaf state, история не нужна.
- **Параллельные регионы** — не вводятся. Независимое измерение агрегата остаётся отдельным
  полем или вложенной entity со своей таблицей (lifecycle Preparation Item). Регионы
  потребуют отдельного решения, когда один trigger должен атомарно менять несколько
  независимых измерений одного aggregate.
- **Библиотеки statecharts в domain** — не используются, см.
  [рассмотренные библиотеки](#рассмотренные-библиотеки). Engine — небольшая собственная
  реализация в каждом языке над явными данными; это то же требование ADR 21 к библиотеке.

### Рассмотренные библиотеки

Требования к библиотеке (из ADR 21 и этого ADR): таблица и иерархия доступны как данные для
теста полноты и диаграмм; восстановление из persisted leaf без побочных эффектов; три
различимых исхода `InvalidTransition` / `GuardFailed{reasons}` / `AlreadyInState`;
compound state без обязательного initial child, так как он используется только как группа.
Проверено 2026-10-26:

| Язык | Библиотека | Иерархия, данные, восстановление | Мешает |
| --- | --- | --- | --- |
| TS | XState 5.33 (`transition()` — чистая функция с 5.19, `machine.resolveState`) | Иерархия, config — данные, восстановление есть, actions возвращаются, а не выполняются | Проверено прогоном: guard=false и необработанное событие возвращают тот же snapshot без причины — `GuardFailed{reasons}` и `InvalidTransition` неразличимы; каждый compound state обязан иметь `initial`; рантайм тянет actors/actions/delays в домен |
| Go | qmuntal/stateless | `SubstateOf`, external state storage, `IsInState`, DOT-граф | Guard — `bool` без причины; необработанный trigger — panic либо `OnUnhandledTrigger`; мутирующий `Fire`, entry/exit — центральная модель API |
| Go | dragomit/hsm, stateforward/hsm.go | UML-иерархия, history, регионы | Event-driven runtime с actions; таблица не экспортируется как данные |
| Rust | statig 0.4 | Superstates через `Outcome::Super` | Переходы — код обработчиков в макросах, не данные; нет интроспекции для теста полноты; неявный `Top` обрабатывает всё; outcome без типизированных ошибок |
| Python | python-statemachine 3.x (`StateChart`, SCXML-семантика, Mermaid) | Compound/parallel/history, валидация модели, диаграммы | Экземпляр машины с callbacks и event loop; guard — `bool`; compound активирует initial child |
| Python | Sismic | Полный UML 2, YAML, тестовые инструменты | Интерпретатор с runtime, LGPL-3.0 |

Ни одна библиотека не даёт типизированный `GuardFailed{reasons}` и явный `AlreadyInState` без
обёртки, которая заново реализует разрешение перехода. Собственный engine — ~40 строк на
язык; разрешение innermost-first тривиально, а таблица остаётся данными. Решение — собственный
engine в каждом языке; библиотеки остаются кандидатами для UI-потоков, где нужен
interpreter с actions и delays, — это отдельное решение вне ADR.

### Тесты и диаграммы

Тест полноты ADR 21 сохраняется и считается по **leaf states × triggers**: ожидаемая матрица
пишется независимо от таблицы, поэтому перевод плоской таблицы в иерархию доказывается
неизменной матрицей. Дополнительно структурные тесты проверяют: иерархия без циклов, у
compound state есть потомки, пара `(node, trigger)` уникальна, строки нет на корне, повторы
объявлены только на leaf и не совпадают со строкой перехода того же leaf.

Диаграммы в документации рисуются как composite states Mermaid `stateDiagram-v2`.

### Порядок перехода

Пилот — `RestaurantOrder` в `restaurants` (наибольшее дублирование). Затем `Delivery`,
`Order`, `Payment`; для каждого агрегата матрица `state × trigger` не меняется, ручные наборы
состояний заменяются принадлежностью.

## Consequences

Одно бизнес-правило записывается один раз на нужном уровне иерархии. Новый state, добавленный
в compound state, автоматически получает его переходы и попадает во все фильтры — но именно
поэтому включение state в группу становится осознанным решением, проверяемым матрицей
полноты.

Чтение таблицы требует учитывать предков: исход для пары `(leaf, trigger)` больше не виден в
одной строке. Компенсация — независимая матрица в тесте и composite-диаграмма.

Внешние контракты не меняются: хранится и публикуется leaf state, поэтому миграции данных,
изменения Protobuf и событий не нужны.

Engine остаётся собственным в каждом из четырёх языков; общая семантика задаётся этим ADR и
[документацией FSM](../fsm.md#statecharts-иерархия-состояний), а не общей библиотекой.
