Feature: Record courier release

  @AC-534
  Scenario: A courier removed without replacement loses access to the order
    Given a courier is assigned to the delivery of an order
    When delivery removes the courier without a replacement
    Then the courier can no longer view the order
    And the order keeps its delivery

  @AC-535
  Scenario: A cancelled delivery releases the courier and the delivery
    Given a courier is assigned to the delivery of an order
    When delivery cancels the delivery
    Then neither the courier nor the delivery can view the order

  @AC-536
  Scenario: An event about another assignment changes nothing
    Given a courier is assigned to the delivery of an order
    When delivery reports the release of another courier or delivery
    Then the assigned courier still views the order
