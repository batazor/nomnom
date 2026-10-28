package main

import (
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/courierpool/applications"
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
)

var (
	poolAvailabilities = map[applications.Availability]service.CourierAvailability{
		applications.AvailabilityAvailable:   service.CourierAvailability_COURIER_AVAILABILITY_AVAILABLE,
		applications.AvailabilityBusy:        service.CourierAvailability_COURIER_AVAILABILITY_BUSY,
		applications.AvailabilityUnavailable: service.CourierAvailability_COURIER_AVAILABILITY_UNAVAILABLE,
	}
	courierPoolProblems = map[applications.Problem]service.CourierPoolProblemCode{
		applications.ProblemCourierNameInvalid:    service.CourierPoolProblemCode_COURIER_POOL_PROBLEM_CODE_COURIER_NAME_INVALID,
		applications.ProblemCourierStatusConflict: service.CourierPoolProblemCode_COURIER_POOL_PROBLEM_CODE_COURIER_STATUS_CONFLICT,
		applications.ProblemCourierOnLine:         service.CourierPoolProblemCode_COURIER_POOL_PROBLEM_CODE_COURIER_ON_LINE,
		applications.ProblemOfferRefused:          service.CourierPoolProblemCode_COURIER_POOL_PROBLEM_CODE_OFFER_REFUSED,
	}
)

func courierPoolMessage(pool applications.Pool) (*service.CourierPool, error) {
	message := &service.CourierPool{
		Id:       pool.Market,
		Size:     int32(pool.Size),
		OnLine:   int32(pool.OnLine),
		Busy:     int32(pool.Busy),
		Waiting:  int32(pool.Waiting),
		Couriers: make([]*service.PoolCourier, 0, len(pool.Couriers)),
	}
	for _, courier := range pool.Couriers {
		availability, err := enumValue(poolAvailabilities, "courier availability", courier.Availability)
		if err != nil {
			return nil, err
		}
		entry := &service.PoolCourier{Id: courier.ID, Name: courier.Name, Availability: availability}
		if courier.OfferedOrderID != "" {
			entry.OfferedOrderId = wrapperspb.String(courier.OfferedOrderID)
		}
		if courier.CurrentOrderID != "" {
			entry.CurrentOrderId = wrapperspb.String(courier.CurrentOrderID)
		}
		message.Couriers = append(message.Couriers, entry)
	}
	return message, nil
}

func courierPoolPayload(result applications.Result) (*service.CourierPoolPayload, error) {
	pool, err := courierPoolMessage(result.Pool)
	if err != nil {
		return nil, err
	}
	payload := &service.CourierPoolPayload{CourierPool: pool}
	if result.Problem != "" {
		problem, err := enumValue(courierPoolProblems, "courier pool problem", result.Problem)
		if err != nil {
			return nil, err
		}
		payload.Problem = problem
	}
	return payload, nil
}
