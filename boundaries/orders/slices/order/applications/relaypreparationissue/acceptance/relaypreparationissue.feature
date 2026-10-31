Feature: Relay preparation issue

  @pending
  Scenario: A preparation issue reaches order fulfillment
    Given order fulfillment is running
    When the restaurant reports a preparation issue
    Then order fulfillment is told of the issue and its reason

  @pending
  Scenario: A preparation issue after order fulfillment is closed is not an error
    Given order fulfillment is closed
    When the restaurant reports a preparation issue
    Then nobody is told of the issue
