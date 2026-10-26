# Assign Cook

## Сценарий

- **Тип:** Application service.
- **Actor:** Start Preparation слайса `restaurantorder` (порт `CookAssigner`).
- **Намерение:** Назначить заказу свободного повара на линии.
- **Результат:** Свободный повар на линии с самым ранним последним назначением (никогда не назначенные — первыми) получает `lastAssignedAt`; его документ сохраняется в Unit of Work команды, поэтому два старта на одного повара конфликтуют. Нет свободного — `NoCookAvailableError`.

## Границы

- **Владелец:** `restaurants/slices/cook`.
- **Домен:** `domains/cook`.
- **Взаимодействие:** Занятость повара и очередь читаются из Restaurant Orders (`CookWorkload`); запись повара отправляет Change Notification kitchen board его ресторана (ADR 36). Integration Events не публикуются.

## Acceptance

FR-225, FR-617, AC-782–AC-785 change [`add-cook-and-courier-pools`](../../../../../../openspec/changes/add-cook-and-courier-pools/).

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).
