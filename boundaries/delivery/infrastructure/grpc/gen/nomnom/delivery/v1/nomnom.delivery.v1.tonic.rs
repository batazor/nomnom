// @generated
/// Generated client implementations.
pub mod delivery_service_client {
    #![allow(
        unused_variables,
        dead_code,
        missing_docs,
        clippy::wildcard_imports,
        clippy::let_unit_value,
    )]
    use tonic::codegen::*;
    use tonic::codegen::http::Uri;
    #[derive(Debug, Clone)]
    pub struct DeliveryServiceClient<T> {
        inner: tonic::client::Grpc<T>,
    }
    impl DeliveryServiceClient<tonic::transport::Channel> {
        /// Attempt to create a new client by connecting to a given endpoint.
        pub async fn connect<D>(dst: D) -> Result<Self, tonic::transport::Error>
        where
            D: TryInto<tonic::transport::Endpoint>,
            D::Error: Into<StdError>,
        {
            let conn = tonic::transport::Endpoint::new(dst)?.connect().await?;
            Ok(Self::new(conn))
        }
    }
    impl<T> DeliveryServiceClient<T>
    where
        T: tonic::client::GrpcService<tonic::body::Body>,
        T::Error: Into<StdError>,
        T::ResponseBody: Body<Data = Bytes> + std::marker::Send + 'static,
        <T::ResponseBody as Body>::Error: Into<StdError> + std::marker::Send,
    {
        pub fn new(inner: T) -> Self {
            let inner = tonic::client::Grpc::new(inner);
            Self { inner }
        }
        pub fn with_origin(inner: T, origin: Uri) -> Self {
            let inner = tonic::client::Grpc::with_origin(inner, origin);
            Self { inner }
        }
        pub fn with_interceptor<F>(
            inner: T,
            interceptor: F,
        ) -> DeliveryServiceClient<InterceptedService<T, F>>
        where
            F: tonic::service::Interceptor,
            T::ResponseBody: Default,
            T: tonic::codegen::Service<
                http::Request<tonic::body::Body>,
                Response = http::Response<
                    <T as tonic::client::GrpcService<tonic::body::Body>>::ResponseBody,
                >,
            >,
            <T as tonic::codegen::Service<
                http::Request<tonic::body::Body>,
            >>::Error: Into<StdError> + std::marker::Send + std::marker::Sync,
        {
            DeliveryServiceClient::new(InterceptedService::new(inner, interceptor))
        }
        /// Compress requests with the given encoding.
        ///
        /// This requires the server to support it otherwise it might respond with an
        /// error.
        #[must_use]
        pub fn send_compressed(mut self, encoding: CompressionEncoding) -> Self {
            self.inner = self.inner.send_compressed(encoding);
            self
        }
        /// Enable decompressing responses.
        #[must_use]
        pub fn accept_compressed(mut self, encoding: CompressionEncoding) -> Self {
            self.inner = self.inner.accept_compressed(encoding);
            self
        }
        /// Limits the maximum size of a decoded message.
        ///
        /// Default: `4MB`
        #[must_use]
        pub fn max_decoding_message_size(mut self, limit: usize) -> Self {
            self.inner = self.inner.max_decoding_message_size(limit);
            self
        }
        /// Limits the maximum size of an encoded message.
        ///
        /// Default: `usize::MAX`
        #[must_use]
        pub fn max_encoding_message_size(mut self, limit: usize) -> Self {
            self.inner = self.inner.max_encoding_message_size(limit);
            self
        }
        pub async fn get_courier_current_delivery(
            &mut self,
            request: impl tonic::IntoRequest<super::GetCourierCurrentDeliveryRequest>,
        ) -> std::result::Result<
            tonic::Response<super::GetCourierCurrentDeliveryResponse>,
            tonic::Status,
        > {
            self.inner
                .ready()
                .await
                .map_err(|e| {
                    tonic::Status::unknown(
                        format!("Service was not ready: {}", e.into()),
                    )
                })?;
            let codec = tonic_prost::ProstCodec::default();
            let path = http::uri::PathAndQuery::from_static(
                "/nomnom.delivery.v1.DeliveryService/GetCourierCurrentDelivery",
            );
            let mut req = request.into_request();
            req.extensions_mut()
                .insert(
                    GrpcMethod::new(
                        "nomnom.delivery.v1.DeliveryService",
                        "GetCourierCurrentDelivery",
                    ),
                );
            self.inner.unary(req, path, codec).await
        }
        pub async fn get_order_courier(
            &mut self,
            request: impl tonic::IntoRequest<super::GetOrderCourierRequest>,
        ) -> std::result::Result<
            tonic::Response<super::GetOrderCourierResponse>,
            tonic::Status,
        > {
            self.inner
                .ready()
                .await
                .map_err(|e| {
                    tonic::Status::unknown(
                        format!("Service was not ready: {}", e.into()),
                    )
                })?;
            let codec = tonic_prost::ProstCodec::default();
            let path = http::uri::PathAndQuery::from_static(
                "/nomnom.delivery.v1.DeliveryService/GetOrderCourier",
            );
            let mut req = request.into_request();
            req.extensions_mut()
                .insert(
                    GrpcMethod::new(
                        "nomnom.delivery.v1.DeliveryService",
                        "GetOrderCourier",
                    ),
                );
            self.inner.unary(req, path, codec).await
        }
        pub async fn create_delivery(
            &mut self,
            request: impl tonic::IntoRequest<super::CreateDeliveryRequest>,
        ) -> std::result::Result<
            tonic::Response<super::CreateDeliveryResponse>,
            tonic::Status,
        > {
            self.inner
                .ready()
                .await
                .map_err(|e| {
                    tonic::Status::unknown(
                        format!("Service was not ready: {}", e.into()),
                    )
                })?;
            let codec = tonic_prost::ProstCodec::default();
            let path = http::uri::PathAndQuery::from_static(
                "/nomnom.delivery.v1.DeliveryService/CreateDelivery",
            );
            let mut req = request.into_request();
            req.extensions_mut()
                .insert(
                    GrpcMethod::new(
                        "nomnom.delivery.v1.DeliveryService",
                        "CreateDelivery",
                    ),
                );
            self.inner.unary(req, path, codec).await
        }
        pub async fn replan_delivery(
            &mut self,
            request: impl tonic::IntoRequest<super::ReplanDeliveryRequest>,
        ) -> std::result::Result<
            tonic::Response<super::ReplanDeliveryResponse>,
            tonic::Status,
        > {
            self.inner
                .ready()
                .await
                .map_err(|e| {
                    tonic::Status::unknown(
                        format!("Service was not ready: {}", e.into()),
                    )
                })?;
            let codec = tonic_prost::ProstCodec::default();
            let path = http::uri::PathAndQuery::from_static(
                "/nomnom.delivery.v1.DeliveryService/ReplanDelivery",
            );
            let mut req = request.into_request();
            req.extensions_mut()
                .insert(
                    GrpcMethod::new(
                        "nomnom.delivery.v1.DeliveryService",
                        "ReplanDelivery",
                    ),
                );
            self.inner.unary(req, path, codec).await
        }
        pub async fn cancel_delivery(
            &mut self,
            request: impl tonic::IntoRequest<super::CancelDeliveryRequest>,
        ) -> std::result::Result<
            tonic::Response<super::CancelDeliveryResponse>,
            tonic::Status,
        > {
            self.inner
                .ready()
                .await
                .map_err(|e| {
                    tonic::Status::unknown(
                        format!("Service was not ready: {}", e.into()),
                    )
                })?;
            let codec = tonic_prost::ProstCodec::default();
            let path = http::uri::PathAndQuery::from_static(
                "/nomnom.delivery.v1.DeliveryService/CancelDelivery",
            );
            let mut req = request.into_request();
            req.extensions_mut()
                .insert(
                    GrpcMethod::new(
                        "nomnom.delivery.v1.DeliveryService",
                        "CancelDelivery",
                    ),
                );
            self.inner.unary(req, path, codec).await
        }
        pub async fn register_courier(
            &mut self,
            request: impl tonic::IntoRequest<super::RegisterCourierRequest>,
        ) -> std::result::Result<
            tonic::Response<super::RegisterCourierResponse>,
            tonic::Status,
        > {
            self.inner
                .ready()
                .await
                .map_err(|e| {
                    tonic::Status::unknown(
                        format!("Service was not ready: {}", e.into()),
                    )
                })?;
            let codec = tonic_prost::ProstCodec::default();
            let path = http::uri::PathAndQuery::from_static(
                "/nomnom.delivery.v1.DeliveryService/RegisterCourier",
            );
            let mut req = request.into_request();
            req.extensions_mut()
                .insert(
                    GrpcMethod::new(
                        "nomnom.delivery.v1.DeliveryService",
                        "RegisterCourier",
                    ),
                );
            self.inner.unary(req, path, codec).await
        }
        pub async fn retire_courier(
            &mut self,
            request: impl tonic::IntoRequest<super::RetireCourierRequest>,
        ) -> std::result::Result<
            tonic::Response<super::RetireCourierResponse>,
            tonic::Status,
        > {
            self.inner
                .ready()
                .await
                .map_err(|e| {
                    tonic::Status::unknown(
                        format!("Service was not ready: {}", e.into()),
                    )
                })?;
            let codec = tonic_prost::ProstCodec::default();
            let path = http::uri::PathAndQuery::from_static(
                "/nomnom.delivery.v1.DeliveryService/RetireCourier",
            );
            let mut req = request.into_request();
            req.extensions_mut()
                .insert(
                    GrpcMethod::new(
                        "nomnom.delivery.v1.DeliveryService",
                        "RetireCourier",
                    ),
                );
            self.inner.unary(req, path, codec).await
        }
        pub async fn get_courier_pool(
            &mut self,
            request: impl tonic::IntoRequest<super::GetCourierPoolRequest>,
        ) -> std::result::Result<
            tonic::Response<super::GetCourierPoolResponse>,
            tonic::Status,
        > {
            self.inner
                .ready()
                .await
                .map_err(|e| {
                    tonic::Status::unknown(
                        format!("Service was not ready: {}", e.into()),
                    )
                })?;
            let codec = tonic_prost::ProstCodec::default();
            let path = http::uri::PathAndQuery::from_static(
                "/nomnom.delivery.v1.DeliveryService/GetCourierPool",
            );
            let mut req = request.into_request();
            req.extensions_mut()
                .insert(
                    GrpcMethod::new(
                        "nomnom.delivery.v1.DeliveryService",
                        "GetCourierPool",
                    ),
                );
            self.inner.unary(req, path, codec).await
        }
        pub async fn update_courier_location(
            &mut self,
            request: impl tonic::IntoRequest<super::UpdateCourierLocationRequest>,
        ) -> std::result::Result<
            tonic::Response<super::UpdateCourierLocationResponse>,
            tonic::Status,
        > {
            self.inner
                .ready()
                .await
                .map_err(|e| {
                    tonic::Status::unknown(
                        format!("Service was not ready: {}", e.into()),
                    )
                })?;
            let codec = tonic_prost::ProstCodec::default();
            let path = http::uri::PathAndQuery::from_static(
                "/nomnom.delivery.v1.DeliveryService/UpdateCourierLocation",
            );
            let mut req = request.into_request();
            req.extensions_mut()
                .insert(
                    GrpcMethod::new(
                        "nomnom.delivery.v1.DeliveryService",
                        "UpdateCourierLocation",
                    ),
                );
            self.inner.unary(req, path, codec).await
        }
        pub async fn set_courier_availability(
            &mut self,
            request: impl tonic::IntoRequest<super::SetCourierAvailabilityRequest>,
        ) -> std::result::Result<
            tonic::Response<super::SetCourierAvailabilityResponse>,
            tonic::Status,
        > {
            self.inner
                .ready()
                .await
                .map_err(|e| {
                    tonic::Status::unknown(
                        format!("Service was not ready: {}", e.into()),
                    )
                })?;
            let codec = tonic_prost::ProstCodec::default();
            let path = http::uri::PathAndQuery::from_static(
                "/nomnom.delivery.v1.DeliveryService/SetCourierAvailability",
            );
            let mut req = request.into_request();
            req.extensions_mut()
                .insert(
                    GrpcMethod::new(
                        "nomnom.delivery.v1.DeliveryService",
                        "SetCourierAvailability",
                    ),
                );
            self.inner.unary(req, path, codec).await
        }
        pub async fn list_available_delivery_offers(
            &mut self,
            request: impl tonic::IntoRequest<super::ListAvailableDeliveryOffersRequest>,
        ) -> std::result::Result<
            tonic::Response<super::ListAvailableDeliveryOffersResponse>,
            tonic::Status,
        > {
            self.inner
                .ready()
                .await
                .map_err(|e| {
                    tonic::Status::unknown(
                        format!("Service was not ready: {}", e.into()),
                    )
                })?;
            let codec = tonic_prost::ProstCodec::default();
            let path = http::uri::PathAndQuery::from_static(
                "/nomnom.delivery.v1.DeliveryService/ListAvailableDeliveryOffers",
            );
            let mut req = request.into_request();
            req.extensions_mut()
                .insert(
                    GrpcMethod::new(
                        "nomnom.delivery.v1.DeliveryService",
                        "ListAvailableDeliveryOffers",
                    ),
                );
            self.inner.unary(req, path, codec).await
        }
        pub async fn accept_delivery_offer(
            &mut self,
            request: impl tonic::IntoRequest<super::AcceptDeliveryOfferRequest>,
        ) -> std::result::Result<
            tonic::Response<super::AcceptDeliveryOfferResponse>,
            tonic::Status,
        > {
            self.inner
                .ready()
                .await
                .map_err(|e| {
                    tonic::Status::unknown(
                        format!("Service was not ready: {}", e.into()),
                    )
                })?;
            let codec = tonic_prost::ProstCodec::default();
            let path = http::uri::PathAndQuery::from_static(
                "/nomnom.delivery.v1.DeliveryService/AcceptDeliveryOffer",
            );
            let mut req = request.into_request();
            req.extensions_mut()
                .insert(
                    GrpcMethod::new(
                        "nomnom.delivery.v1.DeliveryService",
                        "AcceptDeliveryOffer",
                    ),
                );
            self.inner.unary(req, path, codec).await
        }
        pub async fn reject_delivery_offer(
            &mut self,
            request: impl tonic::IntoRequest<super::RejectDeliveryOfferRequest>,
        ) -> std::result::Result<
            tonic::Response<super::RejectDeliveryOfferResponse>,
            tonic::Status,
        > {
            self.inner
                .ready()
                .await
                .map_err(|e| {
                    tonic::Status::unknown(
                        format!("Service was not ready: {}", e.into()),
                    )
                })?;
            let codec = tonic_prost::ProstCodec::default();
            let path = http::uri::PathAndQuery::from_static(
                "/nomnom.delivery.v1.DeliveryService/RejectDeliveryOffer",
            );
            let mut req = request.into_request();
            req.extensions_mut()
                .insert(
                    GrpcMethod::new(
                        "nomnom.delivery.v1.DeliveryService",
                        "RejectDeliveryOffer",
                    ),
                );
            self.inner.unary(req, path, codec).await
        }
        pub async fn confirm_pickup(
            &mut self,
            request: impl tonic::IntoRequest<super::ConfirmPickupRequest>,
        ) -> std::result::Result<
            tonic::Response<super::ConfirmPickupResponse>,
            tonic::Status,
        > {
            self.inner
                .ready()
                .await
                .map_err(|e| {
                    tonic::Status::unknown(
                        format!("Service was not ready: {}", e.into()),
                    )
                })?;
            let codec = tonic_prost::ProstCodec::default();
            let path = http::uri::PathAndQuery::from_static(
                "/nomnom.delivery.v1.DeliveryService/ConfirmPickup",
            );
            let mut req = request.into_request();
            req.extensions_mut()
                .insert(
                    GrpcMethod::new(
                        "nomnom.delivery.v1.DeliveryService",
                        "ConfirmPickup",
                    ),
                );
            self.inner.unary(req, path, codec).await
        }
        pub async fn confirm_delivery(
            &mut self,
            request: impl tonic::IntoRequest<super::ConfirmDeliveryRequest>,
        ) -> std::result::Result<
            tonic::Response<super::ConfirmDeliveryResponse>,
            tonic::Status,
        > {
            self.inner
                .ready()
                .await
                .map_err(|e| {
                    tonic::Status::unknown(
                        format!("Service was not ready: {}", e.into()),
                    )
                })?;
            let codec = tonic_prost::ProstCodec::default();
            let path = http::uri::PathAndQuery::from_static(
                "/nomnom.delivery.v1.DeliveryService/ConfirmDelivery",
            );
            let mut req = request.into_request();
            req.extensions_mut()
                .insert(
                    GrpcMethod::new(
                        "nomnom.delivery.v1.DeliveryService",
                        "ConfirmDelivery",
                    ),
                );
            self.inner.unary(req, path, codec).await
        }
        pub async fn report_delivery_failure(
            &mut self,
            request: impl tonic::IntoRequest<super::ReportDeliveryFailureRequest>,
        ) -> std::result::Result<
            tonic::Response<super::ReportDeliveryFailureResponse>,
            tonic::Status,
        > {
            self.inner
                .ready()
                .await
                .map_err(|e| {
                    tonic::Status::unknown(
                        format!("Service was not ready: {}", e.into()),
                    )
                })?;
            let codec = tonic_prost::ProstCodec::default();
            let path = http::uri::PathAndQuery::from_static(
                "/nomnom.delivery.v1.DeliveryService/ReportDeliveryFailure",
            );
            let mut req = request.into_request();
            req.extensions_mut()
                .insert(
                    GrpcMethod::new(
                        "nomnom.delivery.v1.DeliveryService",
                        "ReportDeliveryFailure",
                    ),
                );
            self.inner.unary(req, path, codec).await
        }
        pub async fn get_simulator(
            &mut self,
            request: impl tonic::IntoRequest<super::GetSimulatorRequest>,
        ) -> std::result::Result<
            tonic::Response<super::GetSimulatorResponse>,
            tonic::Status,
        > {
            self.inner
                .ready()
                .await
                .map_err(|e| {
                    tonic::Status::unknown(
                        format!("Service was not ready: {}", e.into()),
                    )
                })?;
            let codec = tonic_prost::ProstCodec::default();
            let path = http::uri::PathAndQuery::from_static(
                "/nomnom.delivery.v1.DeliveryService/GetSimulator",
            );
            let mut req = request.into_request();
            req.extensions_mut()
                .insert(
                    GrpcMethod::new("nomnom.delivery.v1.DeliveryService", "GetSimulator"),
                );
            self.inner.unary(req, path, codec).await
        }
        pub async fn set_simulator_enabled(
            &mut self,
            request: impl tonic::IntoRequest<super::SetSimulatorEnabledRequest>,
        ) -> std::result::Result<
            tonic::Response<super::SetSimulatorEnabledResponse>,
            tonic::Status,
        > {
            self.inner
                .ready()
                .await
                .map_err(|e| {
                    tonic::Status::unknown(
                        format!("Service was not ready: {}", e.into()),
                    )
                })?;
            let codec = tonic_prost::ProstCodec::default();
            let path = http::uri::PathAndQuery::from_static(
                "/nomnom.delivery.v1.DeliveryService/SetSimulatorEnabled",
            );
            let mut req = request.into_request();
            req.extensions_mut()
                .insert(
                    GrpcMethod::new(
                        "nomnom.delivery.v1.DeliveryService",
                        "SetSimulatorEnabled",
                    ),
                );
            self.inner.unary(req, path, codec).await
        }
    }
}
/// Generated server implementations.
pub mod delivery_service_server {
    #![allow(
        unused_variables,
        dead_code,
        missing_docs,
        clippy::wildcard_imports,
        clippy::let_unit_value,
    )]
    use tonic::codegen::*;
    /// Generated trait containing gRPC methods that should be implemented for use with DeliveryServiceServer.
    #[async_trait]
    pub trait DeliveryService: std::marker::Send + std::marker::Sync + 'static {
        async fn get_courier_current_delivery(
            &self,
            request: tonic::Request<super::GetCourierCurrentDeliveryRequest>,
        ) -> std::result::Result<
            tonic::Response<super::GetCourierCurrentDeliveryResponse>,
            tonic::Status,
        >;
        async fn get_order_courier(
            &self,
            request: tonic::Request<super::GetOrderCourierRequest>,
        ) -> std::result::Result<
            tonic::Response<super::GetOrderCourierResponse>,
            tonic::Status,
        >;
        async fn create_delivery(
            &self,
            request: tonic::Request<super::CreateDeliveryRequest>,
        ) -> std::result::Result<
            tonic::Response<super::CreateDeliveryResponse>,
            tonic::Status,
        >;
        async fn replan_delivery(
            &self,
            request: tonic::Request<super::ReplanDeliveryRequest>,
        ) -> std::result::Result<
            tonic::Response<super::ReplanDeliveryResponse>,
            tonic::Status,
        >;
        async fn cancel_delivery(
            &self,
            request: tonic::Request<super::CancelDeliveryRequest>,
        ) -> std::result::Result<
            tonic::Response<super::CancelDeliveryResponse>,
            tonic::Status,
        >;
        async fn register_courier(
            &self,
            request: tonic::Request<super::RegisterCourierRequest>,
        ) -> std::result::Result<
            tonic::Response<super::RegisterCourierResponse>,
            tonic::Status,
        >;
        async fn retire_courier(
            &self,
            request: tonic::Request<super::RetireCourierRequest>,
        ) -> std::result::Result<
            tonic::Response<super::RetireCourierResponse>,
            tonic::Status,
        >;
        async fn get_courier_pool(
            &self,
            request: tonic::Request<super::GetCourierPoolRequest>,
        ) -> std::result::Result<
            tonic::Response<super::GetCourierPoolResponse>,
            tonic::Status,
        >;
        async fn update_courier_location(
            &self,
            request: tonic::Request<super::UpdateCourierLocationRequest>,
        ) -> std::result::Result<
            tonic::Response<super::UpdateCourierLocationResponse>,
            tonic::Status,
        >;
        async fn set_courier_availability(
            &self,
            request: tonic::Request<super::SetCourierAvailabilityRequest>,
        ) -> std::result::Result<
            tonic::Response<super::SetCourierAvailabilityResponse>,
            tonic::Status,
        >;
        async fn list_available_delivery_offers(
            &self,
            request: tonic::Request<super::ListAvailableDeliveryOffersRequest>,
        ) -> std::result::Result<
            tonic::Response<super::ListAvailableDeliveryOffersResponse>,
            tonic::Status,
        >;
        async fn accept_delivery_offer(
            &self,
            request: tonic::Request<super::AcceptDeliveryOfferRequest>,
        ) -> std::result::Result<
            tonic::Response<super::AcceptDeliveryOfferResponse>,
            tonic::Status,
        >;
        async fn reject_delivery_offer(
            &self,
            request: tonic::Request<super::RejectDeliveryOfferRequest>,
        ) -> std::result::Result<
            tonic::Response<super::RejectDeliveryOfferResponse>,
            tonic::Status,
        >;
        async fn confirm_pickup(
            &self,
            request: tonic::Request<super::ConfirmPickupRequest>,
        ) -> std::result::Result<
            tonic::Response<super::ConfirmPickupResponse>,
            tonic::Status,
        >;
        async fn confirm_delivery(
            &self,
            request: tonic::Request<super::ConfirmDeliveryRequest>,
        ) -> std::result::Result<
            tonic::Response<super::ConfirmDeliveryResponse>,
            tonic::Status,
        >;
        async fn report_delivery_failure(
            &self,
            request: tonic::Request<super::ReportDeliveryFailureRequest>,
        ) -> std::result::Result<
            tonic::Response<super::ReportDeliveryFailureResponse>,
            tonic::Status,
        >;
        async fn get_simulator(
            &self,
            request: tonic::Request<super::GetSimulatorRequest>,
        ) -> std::result::Result<
            tonic::Response<super::GetSimulatorResponse>,
            tonic::Status,
        >;
        async fn set_simulator_enabled(
            &self,
            request: tonic::Request<super::SetSimulatorEnabledRequest>,
        ) -> std::result::Result<
            tonic::Response<super::SetSimulatorEnabledResponse>,
            tonic::Status,
        >;
    }
    #[derive(Debug)]
    pub struct DeliveryServiceServer<T> {
        inner: Arc<T>,
        accept_compression_encodings: EnabledCompressionEncodings,
        send_compression_encodings: EnabledCompressionEncodings,
        max_decoding_message_size: Option<usize>,
        max_encoding_message_size: Option<usize>,
    }
    impl<T> DeliveryServiceServer<T> {
        pub fn new(inner: T) -> Self {
            Self::from_arc(Arc::new(inner))
        }
        pub fn from_arc(inner: Arc<T>) -> Self {
            Self {
                inner,
                accept_compression_encodings: Default::default(),
                send_compression_encodings: Default::default(),
                max_decoding_message_size: None,
                max_encoding_message_size: None,
            }
        }
        pub fn with_interceptor<F>(
            inner: T,
            interceptor: F,
        ) -> InterceptedService<Self, F>
        where
            F: tonic::service::Interceptor,
        {
            InterceptedService::new(Self::new(inner), interceptor)
        }
        /// Enable decompressing requests with the given encoding.
        #[must_use]
        pub fn accept_compressed(mut self, encoding: CompressionEncoding) -> Self {
            self.accept_compression_encodings.enable(encoding);
            self
        }
        /// Compress responses with the given encoding, if the client supports it.
        #[must_use]
        pub fn send_compressed(mut self, encoding: CompressionEncoding) -> Self {
            self.send_compression_encodings.enable(encoding);
            self
        }
        /// Limits the maximum size of a decoded message.
        ///
        /// Default: `4MB`
        #[must_use]
        pub fn max_decoding_message_size(mut self, limit: usize) -> Self {
            self.max_decoding_message_size = Some(limit);
            self
        }
        /// Limits the maximum size of an encoded message.
        ///
        /// Default: `usize::MAX`
        #[must_use]
        pub fn max_encoding_message_size(mut self, limit: usize) -> Self {
            self.max_encoding_message_size = Some(limit);
            self
        }
    }
    impl<T, B> tonic::codegen::Service<http::Request<B>> for DeliveryServiceServer<T>
    where
        T: DeliveryService,
        B: Body + std::marker::Send + 'static,
        B::Error: Into<StdError> + std::marker::Send + 'static,
    {
        type Response = http::Response<tonic::body::Body>;
        type Error = std::convert::Infallible;
        type Future = BoxFuture<Self::Response, Self::Error>;
        fn poll_ready(
            &mut self,
            _cx: &mut Context<'_>,
        ) -> Poll<std::result::Result<(), Self::Error>> {
            Poll::Ready(Ok(()))
        }
        fn call(&mut self, req: http::Request<B>) -> Self::Future {
            match req.uri().path() {
                "/nomnom.delivery.v1.DeliveryService/GetCourierCurrentDelivery" => {
                    #[allow(non_camel_case_types)]
                    struct GetCourierCurrentDeliverySvc<T: DeliveryService>(pub Arc<T>);
                    impl<
                        T: DeliveryService,
                    > tonic::server::UnaryService<
                        super::GetCourierCurrentDeliveryRequest,
                    > for GetCourierCurrentDeliverySvc<T> {
                        type Response = super::GetCourierCurrentDeliveryResponse;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<
                                super::GetCourierCurrentDeliveryRequest,
                            >,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as DeliveryService>::get_courier_current_delivery(
                                        &inner,
                                        request,
                                    )
                                    .await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let method = GetCourierCurrentDeliverySvc(inner);
                        let codec = tonic_prost::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/nomnom.delivery.v1.DeliveryService/GetOrderCourier" => {
                    #[allow(non_camel_case_types)]
                    struct GetOrderCourierSvc<T: DeliveryService>(pub Arc<T>);
                    impl<
                        T: DeliveryService,
                    > tonic::server::UnaryService<super::GetOrderCourierRequest>
                    for GetOrderCourierSvc<T> {
                        type Response = super::GetOrderCourierResponse;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::GetOrderCourierRequest>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as DeliveryService>::get_order_courier(&inner, request)
                                    .await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let method = GetOrderCourierSvc(inner);
                        let codec = tonic_prost::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/nomnom.delivery.v1.DeliveryService/CreateDelivery" => {
                    #[allow(non_camel_case_types)]
                    struct CreateDeliverySvc<T: DeliveryService>(pub Arc<T>);
                    impl<
                        T: DeliveryService,
                    > tonic::server::UnaryService<super::CreateDeliveryRequest>
                    for CreateDeliverySvc<T> {
                        type Response = super::CreateDeliveryResponse;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::CreateDeliveryRequest>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as DeliveryService>::create_delivery(&inner, request)
                                    .await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let method = CreateDeliverySvc(inner);
                        let codec = tonic_prost::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/nomnom.delivery.v1.DeliveryService/ReplanDelivery" => {
                    #[allow(non_camel_case_types)]
                    struct ReplanDeliverySvc<T: DeliveryService>(pub Arc<T>);
                    impl<
                        T: DeliveryService,
                    > tonic::server::UnaryService<super::ReplanDeliveryRequest>
                    for ReplanDeliverySvc<T> {
                        type Response = super::ReplanDeliveryResponse;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::ReplanDeliveryRequest>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as DeliveryService>::replan_delivery(&inner, request)
                                    .await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let method = ReplanDeliverySvc(inner);
                        let codec = tonic_prost::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/nomnom.delivery.v1.DeliveryService/CancelDelivery" => {
                    #[allow(non_camel_case_types)]
                    struct CancelDeliverySvc<T: DeliveryService>(pub Arc<T>);
                    impl<
                        T: DeliveryService,
                    > tonic::server::UnaryService<super::CancelDeliveryRequest>
                    for CancelDeliverySvc<T> {
                        type Response = super::CancelDeliveryResponse;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::CancelDeliveryRequest>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as DeliveryService>::cancel_delivery(&inner, request)
                                    .await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let method = CancelDeliverySvc(inner);
                        let codec = tonic_prost::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/nomnom.delivery.v1.DeliveryService/RegisterCourier" => {
                    #[allow(non_camel_case_types)]
                    struct RegisterCourierSvc<T: DeliveryService>(pub Arc<T>);
                    impl<
                        T: DeliveryService,
                    > tonic::server::UnaryService<super::RegisterCourierRequest>
                    for RegisterCourierSvc<T> {
                        type Response = super::RegisterCourierResponse;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::RegisterCourierRequest>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as DeliveryService>::register_courier(&inner, request)
                                    .await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let method = RegisterCourierSvc(inner);
                        let codec = tonic_prost::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/nomnom.delivery.v1.DeliveryService/RetireCourier" => {
                    #[allow(non_camel_case_types)]
                    struct RetireCourierSvc<T: DeliveryService>(pub Arc<T>);
                    impl<
                        T: DeliveryService,
                    > tonic::server::UnaryService<super::RetireCourierRequest>
                    for RetireCourierSvc<T> {
                        type Response = super::RetireCourierResponse;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::RetireCourierRequest>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as DeliveryService>::retire_courier(&inner, request)
                                    .await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let method = RetireCourierSvc(inner);
                        let codec = tonic_prost::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/nomnom.delivery.v1.DeliveryService/GetCourierPool" => {
                    #[allow(non_camel_case_types)]
                    struct GetCourierPoolSvc<T: DeliveryService>(pub Arc<T>);
                    impl<
                        T: DeliveryService,
                    > tonic::server::UnaryService<super::GetCourierPoolRequest>
                    for GetCourierPoolSvc<T> {
                        type Response = super::GetCourierPoolResponse;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::GetCourierPoolRequest>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as DeliveryService>::get_courier_pool(&inner, request)
                                    .await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let method = GetCourierPoolSvc(inner);
                        let codec = tonic_prost::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/nomnom.delivery.v1.DeliveryService/UpdateCourierLocation" => {
                    #[allow(non_camel_case_types)]
                    struct UpdateCourierLocationSvc<T: DeliveryService>(pub Arc<T>);
                    impl<
                        T: DeliveryService,
                    > tonic::server::UnaryService<super::UpdateCourierLocationRequest>
                    for UpdateCourierLocationSvc<T> {
                        type Response = super::UpdateCourierLocationResponse;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::UpdateCourierLocationRequest>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as DeliveryService>::update_courier_location(
                                        &inner,
                                        request,
                                    )
                                    .await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let method = UpdateCourierLocationSvc(inner);
                        let codec = tonic_prost::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/nomnom.delivery.v1.DeliveryService/SetCourierAvailability" => {
                    #[allow(non_camel_case_types)]
                    struct SetCourierAvailabilitySvc<T: DeliveryService>(pub Arc<T>);
                    impl<
                        T: DeliveryService,
                    > tonic::server::UnaryService<super::SetCourierAvailabilityRequest>
                    for SetCourierAvailabilitySvc<T> {
                        type Response = super::SetCourierAvailabilityResponse;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::SetCourierAvailabilityRequest>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as DeliveryService>::set_courier_availability(
                                        &inner,
                                        request,
                                    )
                                    .await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let method = SetCourierAvailabilitySvc(inner);
                        let codec = tonic_prost::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/nomnom.delivery.v1.DeliveryService/ListAvailableDeliveryOffers" => {
                    #[allow(non_camel_case_types)]
                    struct ListAvailableDeliveryOffersSvc<T: DeliveryService>(
                        pub Arc<T>,
                    );
                    impl<
                        T: DeliveryService,
                    > tonic::server::UnaryService<
                        super::ListAvailableDeliveryOffersRequest,
                    > for ListAvailableDeliveryOffersSvc<T> {
                        type Response = super::ListAvailableDeliveryOffersResponse;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<
                                super::ListAvailableDeliveryOffersRequest,
                            >,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as DeliveryService>::list_available_delivery_offers(
                                        &inner,
                                        request,
                                    )
                                    .await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let method = ListAvailableDeliveryOffersSvc(inner);
                        let codec = tonic_prost::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/nomnom.delivery.v1.DeliveryService/AcceptDeliveryOffer" => {
                    #[allow(non_camel_case_types)]
                    struct AcceptDeliveryOfferSvc<T: DeliveryService>(pub Arc<T>);
                    impl<
                        T: DeliveryService,
                    > tonic::server::UnaryService<super::AcceptDeliveryOfferRequest>
                    for AcceptDeliveryOfferSvc<T> {
                        type Response = super::AcceptDeliveryOfferResponse;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::AcceptDeliveryOfferRequest>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as DeliveryService>::accept_delivery_offer(
                                        &inner,
                                        request,
                                    )
                                    .await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let method = AcceptDeliveryOfferSvc(inner);
                        let codec = tonic_prost::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/nomnom.delivery.v1.DeliveryService/RejectDeliveryOffer" => {
                    #[allow(non_camel_case_types)]
                    struct RejectDeliveryOfferSvc<T: DeliveryService>(pub Arc<T>);
                    impl<
                        T: DeliveryService,
                    > tonic::server::UnaryService<super::RejectDeliveryOfferRequest>
                    for RejectDeliveryOfferSvc<T> {
                        type Response = super::RejectDeliveryOfferResponse;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::RejectDeliveryOfferRequest>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as DeliveryService>::reject_delivery_offer(
                                        &inner,
                                        request,
                                    )
                                    .await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let method = RejectDeliveryOfferSvc(inner);
                        let codec = tonic_prost::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/nomnom.delivery.v1.DeliveryService/ConfirmPickup" => {
                    #[allow(non_camel_case_types)]
                    struct ConfirmPickupSvc<T: DeliveryService>(pub Arc<T>);
                    impl<
                        T: DeliveryService,
                    > tonic::server::UnaryService<super::ConfirmPickupRequest>
                    for ConfirmPickupSvc<T> {
                        type Response = super::ConfirmPickupResponse;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::ConfirmPickupRequest>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as DeliveryService>::confirm_pickup(&inner, request)
                                    .await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let method = ConfirmPickupSvc(inner);
                        let codec = tonic_prost::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/nomnom.delivery.v1.DeliveryService/ConfirmDelivery" => {
                    #[allow(non_camel_case_types)]
                    struct ConfirmDeliverySvc<T: DeliveryService>(pub Arc<T>);
                    impl<
                        T: DeliveryService,
                    > tonic::server::UnaryService<super::ConfirmDeliveryRequest>
                    for ConfirmDeliverySvc<T> {
                        type Response = super::ConfirmDeliveryResponse;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::ConfirmDeliveryRequest>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as DeliveryService>::confirm_delivery(&inner, request)
                                    .await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let method = ConfirmDeliverySvc(inner);
                        let codec = tonic_prost::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/nomnom.delivery.v1.DeliveryService/ReportDeliveryFailure" => {
                    #[allow(non_camel_case_types)]
                    struct ReportDeliveryFailureSvc<T: DeliveryService>(pub Arc<T>);
                    impl<
                        T: DeliveryService,
                    > tonic::server::UnaryService<super::ReportDeliveryFailureRequest>
                    for ReportDeliveryFailureSvc<T> {
                        type Response = super::ReportDeliveryFailureResponse;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::ReportDeliveryFailureRequest>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as DeliveryService>::report_delivery_failure(
                                        &inner,
                                        request,
                                    )
                                    .await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let method = ReportDeliveryFailureSvc(inner);
                        let codec = tonic_prost::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/nomnom.delivery.v1.DeliveryService/GetSimulator" => {
                    #[allow(non_camel_case_types)]
                    struct GetSimulatorSvc<T: DeliveryService>(pub Arc<T>);
                    impl<
                        T: DeliveryService,
                    > tonic::server::UnaryService<super::GetSimulatorRequest>
                    for GetSimulatorSvc<T> {
                        type Response = super::GetSimulatorResponse;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::GetSimulatorRequest>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as DeliveryService>::get_simulator(&inner, request).await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let method = GetSimulatorSvc(inner);
                        let codec = tonic_prost::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/nomnom.delivery.v1.DeliveryService/SetSimulatorEnabled" => {
                    #[allow(non_camel_case_types)]
                    struct SetSimulatorEnabledSvc<T: DeliveryService>(pub Arc<T>);
                    impl<
                        T: DeliveryService,
                    > tonic::server::UnaryService<super::SetSimulatorEnabledRequest>
                    for SetSimulatorEnabledSvc<T> {
                        type Response = super::SetSimulatorEnabledResponse;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::SetSimulatorEnabledRequest>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as DeliveryService>::set_simulator_enabled(
                                        &inner,
                                        request,
                                    )
                                    .await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let method = SetSimulatorEnabledSvc(inner);
                        let codec = tonic_prost::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                _ => {
                    Box::pin(async move {
                        let mut response = http::Response::new(
                            tonic::body::Body::default(),
                        );
                        let headers = response.headers_mut();
                        headers
                            .insert(
                                tonic::Status::GRPC_STATUS,
                                (tonic::Code::Unimplemented as i32).into(),
                            );
                        headers
                            .insert(
                                http::header::CONTENT_TYPE,
                                tonic::metadata::GRPC_CONTENT_TYPE,
                            );
                        Ok(response)
                    })
                }
            }
        }
    }
    impl<T> Clone for DeliveryServiceServer<T> {
        fn clone(&self) -> Self {
            let inner = self.inner.clone();
            Self {
                inner,
                accept_compression_encodings: self.accept_compression_encodings,
                send_compression_encodings: self.send_compression_encodings,
                max_decoding_message_size: self.max_decoding_message_size,
                max_encoding_message_size: self.max_encoding_message_size,
            }
        }
    }
    /// Generated gRPC service name
    pub const SERVICE_NAME: &str = "nomnom.delivery.v1.DeliveryService";
    impl<T> tonic::server::NamedService for DeliveryServiceServer<T> {
        const NAME: &'static str = SERVICE_NAME;
    }
}
