Feature: Place order

  @AC-100
  Scenario: A customer places an order from available items
    Given a restaurant with available items
    When the customer places an order
    Then the order is placed for the customer in the market of the restaurant
    And the total is computed from the catalog prices
    And the placement is published with the order
    And the relationships of the order are recorded for synchronization

  @AC-101
  Scenario Outline: An order that cannot be fulfilled is refused
    Given a restaurant with available items
    When the customer orders <request>
    Then the order is refused with the reason <reason>
    And nothing is placed

    Examples:
      | request                 | reason                    |
      | an unavailable item     | order_item_unavailable    |
      | an item not on the menu | order_item_unavailable    |
      | below the minimum total | order_total_below_minimum |
      | above the maximum total | order_total_above_maximum |

  @AC-102
  Scenario Outline: A malformed order is rejected before the catalog is read
    Given a restaurant with available items
    When the customer submits <request>
    Then the order is rejected as malformed
    And the catalog is not read
    And nothing is placed

    Examples:
      | request                  |
      | no items                 |
      | a zero quantity          |
      | the same menu item twice |

  @AC-103
  Scenario: A repeated request returns the same order
    Given a customer has placed an order
    When the customer repeats the request with the same idempotency key
    Then the customer gets the same order back
    And no second order is placed

  @pending
  Scenario: Premium priority adds the fee of the restaurant
    Given a restaurant with a premium fee of 99 RUB
    When the customer places a premium order for 500 RUB
    Then the order keeps the premium fee
    And the total of the order is 599 RUB

  @pending
  Scenario: Premium priority is refused at a restaurant without a fee
    Given a restaurant without a premium fee
    When the customer tries to place a premium order
    Then the order is refused because premium priority is unavailable
    And nothing is placed

  @pending
  Scenario: A scheduled order reserves its delivery window
    Given a restaurant with a free delivery window
    When the customer places an order for that delivery window
    Then the order keeps the reserved delivery window
    And the placement is published with the delivery window

  @pending
  Scenario: A full delivery window refuses the order with alternatives
    Given a restaurant with a full delivery window
    When the customer tries to place an order for that delivery window
    Then the order is refused with the nearest available windows
    And nothing is placed
