package mongo

import (
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/description"
	"go.mongodb.org/mongo-driver/x/bsonx/bsoncore"
	"go.mongodb.org/mongo-driver/x/mongo/driver/wiremessage"
)

// https://github.com/mongodb/mongo/blob/ca57a4d640aee04ef373a50b24e79d85f0bb91a0/src/mongo/client/constants.h#L50
const resultFlagAwaitCapable = wiremessage.ReplyFlag(8)

// hard-coded response, emulating an upstream isMaster response from MongoDB
func IsMasterResponse(responseTo int32, topologyKind description.TopologyKind) (*Message, error) {
	imd, err := isMasterDocument(topologyKind)
	if err != nil {
		return nil, err
	}
	reply := opReply{
		flags:       resultFlagAwaitCapable,
		numReturned: 1,
		documents:   []bsoncore.Document{imd},
	}
	wm := reply.Encode(responseTo)
	return &Message{
		Wm: wm,
		Op: &reply,
	}, nil
}

func isMasterDocument(kind description.TopologyKind) (bsoncore.Document, error) {
	ns := time.Now().UnixNano()
	ms := ns / 1e6
	var doc bson.D
	switch kind {
	case description.Single:
		doc = bson.D{
			{Key: "ismaster", Value: true},
			{Key: "maxBsonObjectSize", Value: 16777216},                  // $numberInt
			{Key: "maxMessageSizeBytes", Value: 48000000},                // $numberInt
			{Key: "maxWriteBatchSize", Value: 100000},                    // $numberInt
			{Key: "localTime", Value: bson.D{{Key: "$date", Value: ms}}}, // $numberLong
			{Key: "logicalSessionTimeoutMinutes", Value: 30},             // $numberInt
			{Key: "maxWireVersion", Value: 25},                           // $numberInt
			{Key: "minWireVersion", Value: 0},                            // $numberInt
			{Key: "readOnly", Value: false},
			{Key: "ok", Value: 1.0},
		}
	case description.Sharded:
		doc = bson.D{
			{Key: "ismaster", Value: true},
			{Key: "msg", Value: "isdbgrid"},
			{Key: "maxBsonObjectSize", Value: 16777216},
			{Key: "maxMessageSizeBytes", Value: 48000000},
			{Key: "maxWriteBatchSize", Value: 100000},
			{Key: "localTime", Value: bson.D{{Key: "$date", Value: ms}}},
			{Key: "logicalSessionTimeoutMinutes", Value: 30},
			{Key: "minWireVersion", Value: 0},
			{Key: "maxWireVersion", Value: 25},
			{Key: "saslSupportedMechs", Value: bson.A{}},
			// {Key: "saslSupportedMechs", Value: bson.A{
			// 	"SCRAM-SHA-1",
			// 	"SCRAM-SHA-256",
			// }},
			{Key: "ok", Value: 1.0},
		}
	case description.LoadBalanced:
		doc = bson.D{
			{Key: "ismaster", Value: true},
			{Key: "msg", Value: "isdbgrid"},
			{Key: "serviceId", Value: primitive.NewObjectID()}, // required for load-balanced mode
			{Key: "maxBsonObjectSize", Value: 16777216},
			{Key: "maxMessageSizeBytes", Value: 48000000},
			{Key: "maxWriteBatchSize", Value: 100000},
			{Key: "localTime", Value: bson.D{{Key: "$date", Value: ms}}},
			{Key: "logicalSessionTimeoutMinutes", Value: 30},
			{Key: "minWireVersion", Value: 0},
			{Key: "maxWireVersion", Value: 25},
			{Key: "ok", Value: 1.0},
		}
	default:
		return nil, fmt.Errorf("unsupported topology kind: %v", kind)
	}
	return bson.Marshal(doc)
}

// BuildInfoResponse creates a buildInfo response that indicates this is mongos
func BuildInfoResponse(responseTo int32) (*Message, error) {
	doc := bson.D{
		{Key: "ismaster", Value: true},
		{Key: "msg", Value: "isdbgrid"},
		{Key: "maxBsonObjectSize", Value: 16777216},
		{Key: "maxMessageSizeBytes", Value: 48000000},
		{Key: "maxWriteBatchSize", Value: 100000},
		{Key: "localTime", Value: bson.D{{Key: "$date", Value: time.Now().UnixNano() / 1e6}}},
		{Key: "logicalSessionTimeoutMinutes", Value: 30},
		{Key: "minWireVersion", Value: 0},
		{Key: "maxWireVersion", Value: 25},
		{Key: "ok", Value: 1.0},
	}

	docBytes, err := bson.Marshal(doc)
	if err != nil {
		return nil, err
	}

	reply := opReply{
		flags:       resultFlagAwaitCapable,
		numReturned: 1,
		documents:   []bsoncore.Document{docBytes},
	}
	wm := reply.Encode(responseTo)
	return &Message{
		Wm: wm,
		Op: &reply,
	}, nil
}

// ErrorResponse creates an error response
func ErrorResponse(responseTo int32, errmsg, codeName string) (*Message, error) {
	doc := bson.D{
		{Key: "ok", Value: 0.0},
		{Key: "errmsg", Value: errmsg},
		{Key: "code", Value: 59}, // CommandNotFound
		{Key: "codeName", Value: codeName},
	}

	docBytes, err := bson.Marshal(doc)
	if err != nil {
		return nil, err
	}

	reply := opReply{
		flags:       0,
		numReturned: 1,
		documents:   []bsoncore.Document{docBytes},
	}
	wm := reply.Encode(responseTo)
	return &Message{
		Wm: wm,
		Op: &reply,
	}, nil
}

// GetParameterResponse creates a getParameter response
func GetParameterResponse(responseTo int32) (*Message, error) {
	doc := bson.D{
		{Key: "featureCompatibilityVersion", Value: bson.D{
			{Key: "version", Value: "6.0"},
		}},
		{Key: "ok", Value: 1.0},
	}

	docBytes, err := bson.Marshal(doc)
	if err != nil {
		return nil, err
	}

	reply := opReply{
		flags:       resultFlagAwaitCapable,
		numReturned: 1,
		documents:   []bsoncore.Document{docBytes},
	}
	wm := reply.Encode(responseTo)
	return &Message{
		Wm: wm,
		Op: &reply,
	}, nil
}

// EmptyCursorResponse creates an empty cursor response
func EmptyCursorResponse(responseTo int32) (*Message, error) {
	doc := bson.D{
		{Key: "cursor", Value: bson.D{
			{Key: "firstBatch", Value: bson.A{}},
			{Key: "id", Value: int64(0)},
			{Key: "ns", Value: "admin.atlascli"},
		}},
		{Key: "ok", Value: 1.0},
	}

	docBytes, err := bson.Marshal(doc)
	if err != nil {
		return nil, err
	}

	reply := opReply{
		flags:       resultFlagAwaitCapable,
		numReturned: 1,
		documents:   []bsoncore.Document{docBytes},
	}
	wm := reply.Encode(responseTo)
	return &Message{
		Wm: wm,
		Op: &reply,
	}, nil
}

// TransactionResponse creates a transaction response (abortTransaction or commitTransaction)
func TransactionResponse(responseTo int32, transactionType string) (*Message, error) {
	doc := bson.D{
		{Key: "ok", Value: 1.0},
	}

	docBytes, err := bson.Marshal(doc)
	if err != nil {
		return nil, err
	}

	reply := opReply{
		flags:       resultFlagAwaitCapable,
		numReturned: 1,
		documents:   []bsoncore.Document{docBytes},
	}
	wm := reply.Encode(responseTo)
	return &Message{
		Wm: wm,
		Op: &reply,
	}, nil
}
