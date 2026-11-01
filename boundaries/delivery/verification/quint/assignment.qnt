// The courier assignment protocol of Delivery: Advance Assignment, offers, answers,
// Reassign, Cancel, the Temporal timers and wakes, the SpiceDB grant and the reconciler.
//
// A transaction is split into a read and a commit, so every interleaving of two use
// cases is explored: `save` checks the version read at the start, an insert checks
// nothing, as `WHERE version = $2` in the repository does (ADR 1 of the context).
// A lost `wake` is a late one: the caller repeats the request until it passes (FR-707).
//
// Hand-written, so nothing ties it to the code: see verification/README.md.
module assignment {
  type DeliveryState = Waiting | Assigned | Closed
  type CourierStatus = Available | Busy
  type OfferState = Pending | Accepted | Rejected | Withdrawn | Expired
  type TxKind = Accept | Release | Advance | Offer

  type Delivery = { state: DeliveryState, courier: int, version: int, assignments: int, releases: int }
  type Courier = { status: CourierStatus, version: int }
  type DeliveryOffer = { delivery: int, courier: int, state: OfferState, past: bool, version: int }

  // A transaction in flight: what it read, and whether it holds the courier row.
  type Tx = {
    kind: TxKind,
    delivery: int,
    courier: int,
    offer: int,
    deliveryVersion: int,
    courierVersion: int,
    offerVersion: int,
    closing: bool,
    closes: Set[(int, int)],
    holdsCourier: bool,
  }

  type World = {
    deliveries: int -> Delivery,
    couriers: int -> Courier,
    offers: int -> DeliveryOffer,
    nextOffer: int,
    grants: Set[(int, int)],
    txs: str -> Tx,
    wakes: Set[int],
    candidateWakes: Set[int],
    timers: Set[int],
  }

  pure val DELIVERIES = Set(1, 2, 3)
  pure val COURIERS = Set(1, 2)
  pure val CALLERS = Set("caller1", "caller2")
  pure val MAX_OFFERS = 6
  pure val NO_COURIER = 0

  var s: World

  pure def without(txs: str -> Tx, a: str): str -> Tx =
    txs.keys().exclude(Set(a)).mapBy(other => txs.get(other))

  // Reads.

  def offersOf(d: int): Set[int] =
    s.offers.keys().filter(o => s.offers.get(o).delivery == d)

  def isOpen(o: int): bool =
    s.offers.get(o).state == Pending and not(s.offers.get(o).past)

  def openOffersOf(d: int): Set[int] = offersOf(d).filter(o => isOpen(o))

  def pendingOffersOf(d: int): Set[int] =
    offersOf(d).filter(o => s.offers.get(o).state == Pending)

  def pendingVersions(d: int): Set[(int, int)] =
    pendingOffersOf(d).map(o => (o, s.offers.get(o).version))

  def offeredTo(d: int, c: int): bool =
    offersOf(d).exists(o => s.offers.get(o).courier == c)

  // Who Find Courier may select for the delivery.
  def candidates(d: int): Set[int] =
    COURIERS.filter(c => s.couriers.get(c).status == Available and not(offeredTo(d, c)))

  def courierHeld(c: int): bool =
    s.txs.keys().exists(a => s.txs.get(a).holdsCourier and s.txs.get(a).courier == c)

  def versionsMatch(closes: Set[(int, int)]): bool =
    closes.forall(pair => s.offers.get(pair._1).version == pair._2)

  pure def smallest(values: Set[int]): int =
    values.fold(1000, (least, value) => if (value < least) value else least)

  def closeOffers(
    offers: int -> DeliveryOffer, closes: Set[(int, int)], state: OfferState
  ): int -> DeliveryOffer =
    closes.fold(offers, (result, pair) =>
      result.setBy(pair._1, o => { ...o, state: state, version: o.version + 1 }))

  action startTx(a: str, tx: Tx): bool = s' = { ...s, txs: s.txs.put(a, tx) }

  pure val NO_TX: Tx = {
    kind: Offer, delivery: 0, courier: NO_COURIER, offer: 0, deliveryVersion: 0,
    courierVersion: 0, offerVersion: 0, closing: false, closes: Set(), holdsCourier: false,
  }

  // Time and timers: the deadline of a pending offer passes at any moment.

  action deadlinePasses(o: int): bool = all {
    s.offers.get(o).state == Pending,
    not(s.offers.get(o).past),
    s' = { ...s, offers: s.offers.setBy(o, offer => { ...offer, past: true }) },
  }

  // Expire Delivery Offer, then the workflow takes the next step.
  action timerFires(o: int): bool = {
    val offer = s.offers.get(o)
    all {
      s.timers.contains(o),
      offer.past or offer.state != Pending,
      s' = {
        ...s,
        timers: s.timers.exclude(Set(o)),
        wakes: s.wakes.union(Set(offer.delivery)),
        candidateWakes:
          if (offer.state == Pending or offer.state == Expired)
            s.candidateWakes.union(Set(offer.courier))
          else s.candidateWakes,
        offers:
          if (offer.state == Pending)
            s.offers.set(o, { ...offer, state: Expired, version: offer.version + 1 })
          else s.offers,
      },
    }
  }

  // Assignment Workflow: one Advance Assignment step for a woken delivery.

  action advanceBegin(d: int): bool = {
    val delivery = s.deliveries.get(d)
    val actor = "workflow"
    val woken = { ...s, wakes: s.wakes.exclude(Set(d)) }
    val past = pendingOffersOf(d).filter(o => s.offers.get(o).past)
    val closes = past.map(o => (o, s.offers.get(o).version))
    all {
      s.wakes.contains(d),
      not(s.txs.keys().contains(actor)),
      if (delivery.state != Waiting)
        s' = woken
      else if (openOffersOf(d) != Set())
        s' = { ...woken, timers: s.timers.union(Set(smallest(openOffersOf(d)))) }
      else if (candidates(d) == Set())
        s' = { ...woken, offers: closeOffers(s.offers, closes, Expired) }
      else {
        nondet c = oneOf(candidates(d))
        s' = {
          ...woken,
          txs: s.txs.put(actor, {
            ...NO_TX, kind: Advance, delivery: d, courier: c,
            deliveryVersion: delivery.version, closes: closes,
          }),
        }
      },
    }
  }

  // The step saves the delivery at the version it read; a conflict repeats the step.
  action advanceCommit: bool = {
    val actor = "workflow"
    all {
      s.txs.keys().contains(actor),
      s.nextOffer < MAX_OFFERS,
      val tx = s.txs.get(actor)
      val d = tx.delivery
      val delivery = s.deliveries.get(d)
      val ok = delivery.version == tx.deliveryVersion and versionsMatch(tx.closes)
        and s.couriers.get(tx.courier).status == Available
      val done = { ...s, txs: without(s.txs, actor) }
      val open = openOffersOf(d).filter(o => s.offers.get(o).courier == tx.courier)
      if (ok and open != Set())
        // `make_offer` returns the open offer a manual offer made after the read.
        s' = {
          ...done,
          deliveries: s.deliveries.set(d, { ...delivery, version: delivery.version + 1 }),
          offers: closeOffers(s.offers, tx.closes, Expired),
          timers: s.timers.union(open),
        }
      else if (ok)
        s' = {
          ...done,
          deliveries: s.deliveries.set(d, { ...delivery, version: delivery.version + 1 }),
          offers: closeOffers(s.offers, tx.closes, Expired).put(s.nextOffer, {
            delivery: d, courier: tx.courier, state: Pending, past: false, version: 0,
          }),
          timers: s.timers.union(Set(s.nextOffer)),
          nextOffer: s.nextOffer + 1,
        }
      else
        s' = { ...done, wakes: s.wakes.union(Set(d)) },
    }
  }

  // Offer Delivery / Offer Delivery To Courier: `make_offer` under the lock of the delivery,
  // at the version it read and without a write to it; an open offer of the pair is returned.

  action offerBegin(a: str, d: int, c: int): bool = all {
    not(s.txs.keys().contains(a)),
    s.deliveries.get(d).state == Waiting,
    s.couriers.get(c).status == Available,
    not(openOffersOf(d).exists(o => s.offers.get(o).courier == c)),
    startTx(a, { ...NO_TX, kind: Offer, delivery: d, courier: c,
      deliveryVersion: s.deliveries.get(d).version }),
  }

  action offerCommit(a: str): bool = all {
    s.txs.keys().contains(a),
    s.txs.get(a).kind == Offer,
    s.nextOffer < MAX_OFFERS,
    val tx = s.txs.get(a)
    val done = { ...s, txs: without(s.txs, a) }
    val current = s.deliveries.get(tx.delivery).version == tx.deliveryVersion
    val open = openOffersOf(tx.delivery).exists(o => s.offers.get(o).courier == tx.courier)
    if (current and not(open))
      s' = {
        ...done,
        offers: s.offers.put(s.nextOffer, {
          delivery: tx.delivery, courier: tx.courier, state: Pending, past: false, version: 0,
        }),
        nextOffer: s.nextOffer + 1,
        wakes: s.wakes.union(Set(tx.delivery)),
      }
    else
      s' = done,
  }

  // Reject Delivery Offer: one write, then a wake.

  action reject(o: int): bool = {
    val offer = s.offers.get(o)
    all {
      isOpen(o),
      s' = {
        ...s,
        offers: s.offers.set(o, { ...offer, state: Rejected, version: offer.version + 1 }),
        wakes: s.wakes.union(Set(offer.delivery)),
        candidateWakes: s.candidateWakes.union(Set(offer.courier)),
      },
    }
  }

  // Accept Delivery Offer: read, then one step under the lock of the delivery. Every
  // conflict surfaces before the grant, so a loser writes nothing. A failed commit after
  // the grant is left to the reconciler and not modelled.

  action acceptBegin(a: str, o: int): bool = {
    val offer = s.offers.get(o)
    val delivery = s.deliveries.get(offer.delivery)
    all {
      not(s.txs.keys().contains(a)),
      isOpen(o),
      delivery.state == Waiting,
      s.couriers.get(offer.courier).status == Available,
      startTx(a, {
        ...NO_TX, kind: Accept, delivery: offer.delivery, courier: offer.courier, offer: o,
        deliveryVersion: delivery.version,
        courierVersion: s.couriers.get(offer.courier).version,
        offerVersion: offer.version,
      }),
    }
  }

  action acceptCommit(a: str): bool = all {
    s.txs.keys().contains(a),
    s.txs.get(a).kind == Accept,
    not(courierHeld(s.txs.get(a).courier)),
    val tx = s.txs.get(a)
    val delivery = s.deliveries.get(tx.delivery)
    val offer = s.offers.get(tx.offer)
    val others = pendingOffersOf(tx.delivery).exclude(Set(tx.offer))
      .map(other => (other, s.offers.get(other).version))
    val ok = delivery.version == tx.deliveryVersion and offer.version == tx.offerVersion
      and s.couriers.get(tx.courier).version == tx.courierVersion
    val done = { ...s, txs: without(s.txs, a) }
    if (ok)
      s' = {
        ...done,
        deliveries: s.deliveries.set(tx.delivery, {
          ...delivery, state: Assigned, courier: tx.courier, version: delivery.version + 1,
          assignments: delivery.assignments + 1,
        }),
        couriers: s.couriers.setBy(tx.courier, c => { status: Busy, version: c.version + 1 }),
        offers: closeOffers(s.offers, others, Withdrawn)
          .set(tx.offer, { ...offer, state: Accepted, version: offer.version + 1 }),
        grants: s.grants.union(Set((tx.delivery, tx.courier))),
        wakes: s.wakes.union(Set(tx.delivery)),
      }
    else
      s' = done,
  }

  // Reassign Courier and Cancel Delivery of an assigned delivery: read, release the
  // courier and delete the grant, commit.

  action releaseBegin(a: str, d: int, closing: bool): bool = {
    val delivery = s.deliveries.get(d)
    all {
      not(s.txs.keys().contains(a)),
      delivery.state == Assigned,
      startTx(a, {
        ...NO_TX, kind: Release, delivery: d, courier: delivery.courier,
        deliveryVersion: delivery.version,
        courierVersion: s.couriers.get(delivery.courier).version,
        closing: closing,
      }),
    }
  }

  action releaseRevoke(a: str): bool = all {
    s.txs.keys().contains(a),
    s.txs.get(a).kind == Release,
    not(s.txs.get(a).holdsCourier),
    val tx = s.txs.get(a)
    all {
      not(courierHeld(tx.courier)),
      if (s.couriers.get(tx.courier).version != tx.courierVersion)
        s' = { ...s, txs: without(s.txs, a) }
      else
        s' = {
          ...s,
          txs: s.txs.set(a, { ...tx, holdsCourier: true }),
          grants: s.grants.exclude(Set((tx.delivery, tx.courier))),
        },
    },
  }

  action releaseCommit(a: str): bool = all {
    s.txs.keys().contains(a),
    s.txs.get(a).kind == Release,
    s.txs.get(a).holdsCourier,
    val tx = s.txs.get(a)
    val delivery = s.deliveries.get(tx.delivery)
    val done = { ...s, txs: without(s.txs, a) }
    if (delivery.version == tx.deliveryVersion)
      s' = {
        ...done,
        deliveries: s.deliveries.set(tx.delivery, {
          ...delivery,
          state: if (tx.closing) Closed else Waiting,
          courier: NO_COURIER,
          version: delivery.version + 1,
          releases: delivery.releases + 1,
        }),
        couriers: s.couriers.setBy(tx.courier, c => { status: Available, version: c.version + 1 }),
        offers:
          if (tx.closing) closeOffers(s.offers, pendingVersions(tx.delivery), Withdrawn)
          else s.offers,
        wakes: s.wakes.union(Set(tx.delivery)),
        candidateWakes: s.candidateWakes.union(Set(tx.courier)),
      }
    else
      s' = done,
  }

  // Cancel Delivery of a waiting delivery.
  action cancelWaiting(d: int): bool = {
    val delivery = s.deliveries.get(d)
    all {
      delivery.state == Waiting,
      s' = {
        ...s,
        deliveries: s.deliveries.set(d, { ...delivery, state: Closed, version: delivery.version + 1 }),
        offers: closeOffers(s.offers, pendingVersions(d), Withdrawn),
        wakes: s.wakes.union(Set(d)),
      },
    }
  }

  // Pickup and Confirm Delivery, as one step: the courier keeps the grant of the
  // delivery and the courier id stays on it.
  action complete(d: int): bool = {
    val delivery = s.deliveries.get(d)
    all {
      delivery.state == Assigned,
      not(courierHeld(delivery.courier)),
      s' = {
        ...s,
        deliveries: s.deliveries.set(d, { ...delivery, state: Closed, version: delivery.version + 1 }),
        couriers: s.couriers.setBy(delivery.courier, c => { status: Available, version: c.version + 1 }),
        wakes: s.wakes.union(Set(d)),
        candidateWakes: s.candidateWakes.union(Set(delivery.courier)),
      },
    }
  }

  // A courier who became a Courier Candidate wakes one delivery: the oldest waiting
  // one of its Market without a pending offer, not offered to it yet (FR-706).
  action candidateWake(c: int): bool = {
    val waiting = DELIVERIES.filter(d =>
      s.deliveries.get(d).state == Waiting
        and pendingOffersOf(d) == Set() and not(offeredTo(d, c)))
    val woken = { ...s, candidateWakes: s.candidateWakes.exclude(Set(c)) }
    all {
      s.candidateWakes.contains(c),
      if (waiting == Set()) s' = woken
      else s' = { ...woken, wakes: s.wakes.union(Set(smallest(waiting))) },
    }
  }

  // The reconciler brings the grants to the business state; its grace keeps it away
  // from a grant of a transaction in flight.
  action reconcile: bool = all {
    not(s.txs.keys().exists(a => s.txs.get(a).holdsCourier)),
    s' = {
      ...s,
      grants: DELIVERIES
        .filter(d => s.deliveries.get(d).courier != NO_COURIER)
        .map(d => (d, s.deliveries.get(d).courier)),
    },
  }

  action init: bool = s' = {
    deliveries: DELIVERIES.mapBy(_ => { state: Waiting, courier: NO_COURIER, version: 0, assignments: 0, releases: 0 }),
    couriers: COURIERS.mapBy(_ => { status: Available, version: 0 }),
    offers: Map(),
    nextOffer: 0,
    grants: Set(),
    txs: Map(),
    wakes: DELIVERIES,
    candidateWakes: Set(),
    timers: Set(),
  }

  action step: bool = any {
    nondet d = oneOf(DELIVERIES) any {
      advanceBegin(d),
      cancelWaiting(d),
      complete(d),
      nondet a = oneOf(CALLERS) nondet c = oneOf(COURIERS) offerBegin(a, d, c),
      nondet a = oneOf(CALLERS) nondet closing = oneOf(Set(true, false)) releaseBegin(a, d, closing),
    },
    advanceCommit,
    nondet c = oneOf(COURIERS) candidateWake(c),
    reconcile,
    nondet a = oneOf(CALLERS) any {
      offerCommit(a), acceptCommit(a), releaseRevoke(a), releaseCommit(a),
    },
    all {
      s.offers.keys() != Set(),
      nondet o = oneOf(s.offers.keys()) any {
        deadlinePasses(o),
        timerFires(o),
        reject(o),
        nondet a = oneOf(CALLERS) acceptBegin(a, o),
      },
    },
  }

  // Invariants.

  // One Courier Assignment of a delivery at a time: two accepted offers never overlap.
  val oneWinner = DELIVERIES.forall(d =>
    s.deliveries.get(d).assignments - s.deliveries.get(d).releases <= 1)

  val assignedCourierBusy = DELIVERIES.forall(d =>
    s.deliveries.get(d).state != Assigned
      or s.couriers.get(s.deliveries.get(d).courier).status == Busy)

  val courierHasOneDelivery = COURIERS.forall(c =>
    DELIVERIES.filter(d => s.deliveries.get(d).state == Assigned and s.deliveries.get(d).courier == c)
      .size() <= 1)

  // "The remaining pending offers are withdrawn" (Accept Delivery Offer).
  val noOpenOfferOfAssigned = DELIVERIES.forall(d =>
    s.deliveries.get(d).state != Assigned or openOffersOf(d) == Set())

  val noOpenOfferOfClosed = DELIVERIES.forall(d =>
    s.deliveries.get(d).state != Closed or openOffersOf(d) == Set())

  // "An open offer for the same pair is returned instead of a new one" (make_offer).
  val oneOpenOfferOfPair = DELIVERIES.forall(d => COURIERS.forall(c =>
    openOffersOf(d).filter(o => s.offers.get(o).courier == c).size() <= 1))

  // A grant only for the courier of the delivery, until the grace of the reconciler.
  val noForeignGrant = s.grants.forall(grant =>
    s.deliveries.get(grant._1).courier == grant._2
      or s.txs.keys().exists(a => s.txs.get(a).holdsCourier and s.txs.get(a).delivery == grant._1))

  // The assigned courier can reach its delivery.
  val assignedHasGrant = DELIVERIES.forall(d =>
    s.deliveries.get(d).state != Assigned
      or s.grants.contains((d, s.deliveries.get(d).courier))
      or s.txs.keys().exists(a => s.txs.get(a).holdsCourier and s.txs.get(a).delivery == d))

  // Nothing is left to happen, yet a waiting delivery has a courier it could offer to.
  // Known to be violated, so `make verify-quint` does not check it: a courier wakes one
  // delivery, and that one may close or take another courier (ADR 2 of the context). The
  // next stored position of the courier, not modelled, wakes the search again.
  val quiescent = s.txs.keys() == Set() and s.wakes == Set()
    and s.candidateWakes == Set() and s.timers == Set()

  val noStuckDelivery = not(quiescent and DELIVERIES.exists(d =>
    s.deliveries.get(d).state == Waiting and openOffersOf(d) == Set() and candidates(d) != Set()))

  action check(condition: bool): bool = all { assert(condition), s' = s }

  action offered(d: int, c: int): bool = offerBegin("caller1", d, c).then(offerCommit("caller1"))

  // A courier who rejects an offer wakes the next delivery that was never offered to it.
  run rejectionWakesNextDeliveryTest =
    init
      .then(offered(3, 1))
      .then(acceptBegin("caller1", 0))
      .then(acceptCommit("caller1"))
      .then(offered(1, 2))
      .then(reject(1))
      .then(offered(2, 2))
      .then(reject(2))
      .then(advanceBegin(1))
      .then(advanceBegin(2))
      .then(advanceBegin(3))
      .then(candidateWake(2))
      .then(complete(3))
      .then(advanceBegin(3))
      .then(candidateWake(1))
      .then(advanceBegin(1))
      .then(advanceCommit)
      .then(reject(3))
      .then(advanceBegin(1))
      .then(candidateWake(1))
      .then(check(s.wakes.contains(2)))
}
