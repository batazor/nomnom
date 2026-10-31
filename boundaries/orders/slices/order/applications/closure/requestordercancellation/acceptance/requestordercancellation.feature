Feature: Request order cancellation

  @AC-116
  Scenario Outline: An admissible request reaches order fulfillment
    Given an order is <state>
    When the customer requests its cancellation
    Then order fulfillment receives the cancellation request
    And the order stays <state>

    Examples:
      | state     |
      | placed    |
      | confirmed |

  @AC-115
  Scenario Outline: A subject without access cannot cancel the order
    Given an order is placed
    When another customer requests its cancellation <situation>
    Then the request is denied
    And order fulfillment receives no request

    Examples:
      | situation                      |
      | without the permission         |
      | while authorization is failing |

  @AC-117
  Scenario: A completed order cannot be cancelled
    Given an order is completed
    When the customer tries to cancel it
    Then the request is refused because of the order state
    And order fulfillment receives no request

  @AC-118
  Scenario: The cancellation window has expired
    Given an order is confirmed and its cancellation window has expired
    When the customer tries to cancel it
    Then the request is refused because the cancellation window has expired
    And order fulfillment receives no request

  @AC-119
  Scenario: A cancelled order is reported as already cancelled
    Given an order is cancelled
    When the customer tries to cancel it
    Then the order is reported as already cancelled
    And order fulfillment receives no request

  @pending
  Scenario: A scheduled order can be cancelled before the cutoff of its delivery window
    Given a scheduled order with a delivery window at 19:00 and a cutoff of 60 minutes
    When the customer requests its cancellation at 17:30
    Then order fulfillment receives the cancellation request

  @pending
  Scenario: A scheduled order cannot be cancelled after the cutoff of its delivery window
    Given a scheduled order with a delivery window at 19:00 and a cutoff of 60 minutes
    When the customer tries to cancel it at 18:10
    Then the request is refused because the cancellation window has expired
    And order fulfillment receives no request
