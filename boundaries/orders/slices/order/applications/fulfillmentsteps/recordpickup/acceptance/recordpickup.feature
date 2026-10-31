Feature: Record pickup

  @AC-110
  Scenario: The fact is recorded once
    Given an order is in fulfillment
    When the courier picks up the order
    Then the order records pickup
    And order fulfillment is told of the fact

  @AC-106
  Scenario: The same report is delivered again
    Given the order has recorded pickup from a report
    When the same report is delivered again
    Then the order does not change again
    And order fulfillment is told of the report again

  @AC-107
  Scenario: The fact is reported again in a new report
    Given an order has already recorded pickup
    When a new report states the same fact
    Then the order still records pickup once
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
