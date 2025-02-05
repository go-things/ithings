package deviceMsgEvent

import (
	"context"
	"github.com/i-Things/things/shared/def"
	"github.com/i-Things/things/shared/errors"
	"github.com/i-Things/things/shared/utils"
	"github.com/i-Things/things/src/dmsvr/internal/domain/device"
	"github.com/i-Things/things/src/dmsvr/internal/domain/service/deviceData"
	"github.com/i-Things/things/src/dmsvr/internal/domain/service/deviceSend"
	"github.com/i-Things/things/src/dmsvr/internal/domain/templateModel"
	"github.com/i-Things/things/src/dmsvr/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
	"strings"
)

type PublishLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
	template *templateModel.Template
	topics   []string
	dreq     deviceSend.DeviceReq
	dd       deviceData.DeviceDataRepo
}

func NewPublishLogic(ctx context.Context, svcCtx *svc.ServiceContext) *PublishLogic {
	return &PublishLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *PublishLogic) initMsg(msg *device.PublishMsg) error {
	var err error
	l.template, err = l.svcCtx.TemplateRepo.GetTemplate(l.ctx, msg.ProductID)
	if err != nil {
		return err
	}
	err = utils.Unmarshal([]byte(msg.Payload), &l.dreq)
	if err != nil {
		return errors.Parameter.AddDetail("things topic is err:" + msg.Topic)
	}
	l.dd = l.svcCtx.DeviceDataRepo
	l.topics = strings.Split(msg.Topic, "/")
	return nil
}

func (l *PublishLogic) DeviceResp(msg *device.PublishMsg, err error, data map[string]interface{}) {
	topic, payload := deviceSend.GenThingDeviceRespData(l.dreq.Method, l.dreq.ClientToken, l.topics, err, data)
	er := l.svcCtx.InnerLink.PublishToDev(l.ctx, topic, payload)
	if er != nil {
		l.Errorf("DeviceResp|PublishToDev failure err:%v", er)
		return
	}
	l.Infof("PublishLogic|DeviceResp|topic:%v payload:%v", topic, string(payload))
	//l.svcCtx.DevClient.DeviceResp(l.dreq.Method, l.dreq.ClientToken, l.topics, err, data)
}

func (l *PublishLogic) HandlePropertyReport(msg *device.PublishMsg) error {
	tp, err := l.dreq.VerifyReqParam(l.template, templateModel.PROPERTY)
	if err != nil {
		l.DeviceResp(msg, err, nil)
		return err
	} else if len(tp) == 0 {
		err := errors.Parameter.AddDetail("need right param")
		l.DeviceResp(msg, err, nil)
		return err
	}
	params := deviceSend.ToVal(tp)
	timeStamp := l.dreq.GetTimeStamp(msg.Timestamp)
	err = l.dd.InsertPropertiesData(l.ctx, l.template, msg.ProductID, msg.DeviceName, params, timeStamp)
	if err != nil {
		l.DeviceResp(msg, errors.Database, nil)
		l.Errorf("HandlePropertyReport|InsertPropertyData|err=%+v", err)
		return err
	}
	l.DeviceResp(msg, errors.OK, nil)
	return nil
}

func (l *PublishLogic) HandlePropertyGetStatus(msg *device.PublishMsg) error {
	respData := make(map[string]interface{}, len(l.template.Properties))
	switch l.dreq.Type {
	case deviceSend.REPORT:
		for id, _ := range l.template.Property {
			data, err := l.dd.GetPropertyDataByID(l.ctx, msg.ProductID, msg.DeviceName, id, def.PageInfo2{
				TimeStart: 0,
				TimeEnd:   0,
				Limit:     1,
			})
			if err != nil {
				l.Errorf("HandlePropertyGetStatus|GetPropertyDataByID|get id:%s|err:%s",
					id, err.Error())
				return err
			}
			if len(data) == 0 {
				l.Slowf("HandlePropertyGetStatus|GetPropertyDataByID|not find id:%s", id)
				continue
			}
			respData[id] = data[0].Param
		}
	default:
		err := errors.Parameter.AddDetailf("not suppot type :%s", l.dreq.Type)
		l.DeviceResp(msg, err, nil)
		return err
	}
	l.DeviceResp(msg, errors.OK, respData)
	return nil
}

func (l *PublishLogic) HandleProperty(msg *device.PublishMsg) error {
	l.Slowf("PublishLogic|HandleProperty")
	switch l.dreq.Method {
	case deviceSend.REPORT, deviceSend.REPORT_INFO:
		return l.HandlePropertyReport(msg)
	case deviceSend.GET_STATUS:
		return l.HandlePropertyGetStatus(msg)
	case deviceSend.CONTROL_REPLY:
		return l.HandleResp(msg)
	default:
		return errors.Method
	}
	return nil
}

func (l *PublishLogic) HandleEvent(msg *device.PublishMsg) error {
	l.Slowf("PublishLogic|HandleEvent")
	dbData := deviceData.EventData{}
	dbData.ID = l.dreq.EventID
	dbData.Type = l.dreq.Type
	if l.dreq.Method != deviceSend.EVENT_POST {
		return errors.Method
	}
	tp, err := l.dreq.VerifyReqParam(l.template, templateModel.EVENT)
	if err != nil {
		l.DeviceResp(msg, err, nil)
		return err
	}
	dbData.Params = deviceSend.ToVal(tp)
	dbData.TimeStamp = l.dreq.GetTimeStamp(msg.Timestamp)

	err = l.dd.InsertEventData(l.ctx, msg.ProductID, msg.DeviceName, &dbData)
	if err != nil {
		l.DeviceResp(msg, errors.Database, nil)
		l.Errorf("InsertEventData|err=%+v", err)
		return errors.Database.AddDetail(err)
	}
	l.DeviceResp(msg, errors.OK, nil)

	return nil
}
func (l *PublishLogic) HandleResp(msg *device.PublishMsg) error {
	l.Slowf("PublishLogic|HandleResp")
	//todo 这里后续需要处理异步获取消息的情况
	return nil
}

func (l *PublishLogic) HandleThing(msg *device.PublishMsg) error {
	l.Slowf("PublishLogic|HandleThing")
	if len(l.topics) < 5 || l.topics[1] != "up" {
		return errors.Parameter.AddDetail("things topic is err:" + msg.Topic)
	}
	switch l.topics[2] {
	case def.PROPERTY_METHOD: //属性上报
		return l.HandleProperty(msg)
	case def.EVENT_METHOD: //事件上报
		return l.HandleEvent(msg)
	case def.ACTION_METHOD: //设备响应行为执行结果
		return l.HandleResp(msg)
	default:
		return errors.Parameter.AddDetail("things topic is err:" + msg.Topic)
	}
	return nil
}
func (l *PublishLogic) HandleOta(msg *device.PublishMsg) error {
	l.Slowf("PublishLogic|HandleOta")
	return nil
}

func (l *PublishLogic) HandleDefault(msg *device.PublishMsg) error {
	l.Slowf("PublishLogic|HandleDefault")
	return nil

}
func (l *PublishLogic) Handle(msg *device.PublishMsg) (err error) {
	l.Infof("PublishLogic|req=%+v", msg)
	err = l.initMsg(msg)
	if err != nil {
		return err
	}
	if len(l.topics) > 1 {
		switch l.topics[0] {
		case "$thing":
			err = l.HandleThing(msg)
		case "$ota":
			err = l.HandleOta(msg)
		case msg.ProductID:
			err = l.HandleDefault(msg)
		default:
			err = errors.Parameter.AddDetailf("not suppot topic :%s", msg.Topic)
		}
	} else {
		err = errors.Parameter.AddDetailf("need topic :%s", msg.Topic)
	}
	l.svcCtx.DeviceLogRepo.Insert(l.ctx, &device.Log{
		ProductID:  msg.ProductID,
		Action:     "publish",
		Timestamp:  l.dreq.GetTimeStamp(msg.Timestamp), // 操作时间
		DeviceName: msg.DeviceName,
		TranceID:   utils.TraceIdFromContext(l.ctx),
		RequestID:  l.dreq.ClientToken,
		Content:    string(msg.Payload),
		Topic:      msg.Topic,
		ResultType: errors.Fmt(err).GetCode(),
	})
	return err
}
