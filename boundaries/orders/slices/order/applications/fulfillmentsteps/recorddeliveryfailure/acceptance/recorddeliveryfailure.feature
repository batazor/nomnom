Feature: Record delivery failure

  @AC-111
  Scenario: The fact is recorded once
    Given an order is in fulfillment
    When delivery reports that the delivery failed
    Then the order records delivery failure
    And order fulfillment is told of the fact and its reason

  @AC-106
  Scenario: The same report is delivered again
    Given the order has recorded delivery failure from a report
    When the same report is delivered again
    Then the order does not change again
    And order fulfillment is told of the report again

  @AC-107
  Scenario: The fact is reported again in a new report
    Given an order has already recorded delivery failure
    When a new report states the same fact
    Then the order still records delivery failure once
    And order fulfillment is told of the new report

  @AC-112
  Scenario Outline: A report about a final order changes nothing
    Given an order is <state>
    When a report arrives after order fulfillment is closed
    Then the order does not change
    And the report counts as processed late

    Examples:
      | state     |
      | completed |
      | cancelled |
