Feature: Record courier release

  @AC-534
  Scenario: A courier removed without replacement leaves the order
    Given a courier is assigned to the delivery of an order
    When delivery removes the courier without a replacement
    Then the order has no courier
    And the order keeps its delivery
    And order fulfillment is not notified of the release

  @AC-535
  Scenario: A cancelled delivery releases the courier and the delivery
    Given a courier is assigned to the delivery of an order
    When delivery cancels the delivery
    Then the order has no courier
    And the order has no delivery

  @AC-536
  Scenario Outline: An event about another assignment changes nothing
    Given a courier is assigned to the delivery of an order
    When delivery reports <event>
    Then the order keeps its courier and its delivery

    Examples:
      | event                                            |
      | the removal of the courier from another delivery |
      | a removal older than the assignment              |
      | the cancellation of another delivery             |
