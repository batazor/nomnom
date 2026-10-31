Feature: Record payment authorization

  @AC-829
  Scenario: The payment authorization is recorded
    Given an order is placed
    When order fulfillment records the payment authorization
    Then the order records the payment authorization at its moment
    And neighbouring contexts are not told of it

  @AC-830
  Scenario: Recording the payment authorization again changes nothing
    Given an order has recorded the payment authorization
    When order fulfillment records the payment authorization again
    Then the order reports it as already recorded
    And the order still records one payment authorization

  @pending @FR-639
  Scenario Outline: An order past placed does not accept the payment authorization
    Given an order is <state>
    When order fulfillment tries to record the payment authorization
    Then the payment authorization is refused
    And the order records no payment authorization

    Examples:
      | state     |
      | confirmed |
      | cancelled |
      | completed |
