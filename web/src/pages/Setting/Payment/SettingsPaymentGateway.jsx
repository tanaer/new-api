/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React, { useEffect, useMemo, useRef, useState } from 'react';
import {
  Button,
  Form,
  Row,
  Col,
  Typography,
  Spin,
  Divider,
  Table,
  Tag,
  Input,
  Switch,
  Select,
  InputNumber,
} from '@douyinfe/semi-ui';
const { Text } = Typography;
import {
  API,
  removeTrailingSlash,
  showError,
  showSuccess,
  verifyJSON,
  buildPaymentChannelDrafts,
  serializePaymentChannels,
  getPaymentScopeLabel,
  PAYMENT_SCOPE_OPTIONS,
} from '../../../helpers';
import { useTranslation } from 'react-i18next';

export default function SettingsPaymentGateway(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [paymentChannels, setPaymentChannels] = useState([]);
  const [inputs, setInputs] = useState({
    PayAddress: '',
    EpayId: '',
    EpayKey: '',
    Price: 7.3,
    MinTopUp: 1,
    TopupGroupRatio: '',
    CustomCallbackAddress: '',
    PayMethods: '[]',
    AmountOptions: '',
    AmountDiscount: '',
    BepusdtApiUrl: '',
    BepusdtApiToken: '',
    BepusdtTradeType: 'usdt.trc20',
    BepusdtFiat: 'CNY',
    BepusdtTimeout: 600,
    BepusdtMinPaymentAmount: 1,
    FutoonApiUrl: '',
    FutoonPid: '',
    FutoonKey: '',
    FutoonNotifyURL: '',
    FutoonReturnURL: '',
    FutoonDefaultDevice: 'pc',
  });
  const [originInputs, setOriginInputs] = useState({});
  const formApiRef = useRef(null);
  const paymentChannelsRef = useRef([]);

  useEffect(() => {
    if (props.options && formApiRef.current) {
      const currentInputs = {
        PayAddress: props.options.PayAddress || '',
        EpayId: props.options.EpayId || '',
        EpayKey: props.options.EpayKey || '',
        Price:
          props.options.Price !== undefined
            ? parseFloat(props.options.Price)
            : 7.3,
        MinTopUp:
          props.options.MinTopUp !== undefined
            ? parseFloat(props.options.MinTopUp)
            : 1,
        TopupGroupRatio: props.options.TopupGroupRatio || '',
        CustomCallbackAddress: props.options.CustomCallbackAddress || '',
        PayMethods: props.options.PayMethods || '[]',
        AmountOptions: props.options.AmountOptions || '',
        AmountDiscount: props.options.AmountDiscount || '',
        BepusdtApiUrl: props.options.BepusdtApiUrl || '',
        BepusdtApiToken: props.options.BepusdtApiToken || '',
        BepusdtTradeType: props.options.BepusdtTradeType || 'usdt.trc20',
        BepusdtFiat: props.options.BepusdtFiat || 'CNY',
        BepusdtTimeout:
          props.options.BepusdtTimeout !== undefined
            ? parseInt(props.options.BepusdtTimeout)
            : 600,
        BepusdtMinPaymentAmount:
          props.options.BepusdtMinPaymentAmount !== undefined
            ? parseFloat(props.options.BepusdtMinPaymentAmount)
            : 1,
        FutoonApiUrl: props.options.FutoonApiUrl || '',
        FutoonPid: props.options.FutoonPid || '',
        FutoonKey: props.options.FutoonKey || '',
        FutoonNotifyURL: props.options.FutoonNotifyURL || '',
        FutoonReturnURL: props.options.FutoonReturnURL || '',
        FutoonDefaultDevice: props.options.FutoonDefaultDevice || 'pc',
      };

      try {
        if (currentInputs.AmountOptions) {
          currentInputs.AmountOptions = JSON.stringify(
            JSON.parse(currentInputs.AmountOptions),
            null,
            2,
          );
        }
      } catch {}
      try {
        if (currentInputs.AmountDiscount) {
          currentInputs.AmountDiscount = JSON.stringify(
            JSON.parse(currentInputs.AmountDiscount),
            null,
            2,
          );
        }
      } catch {}

      const channelDrafts = buildPaymentChannelDrafts(currentInputs.PayMethods);
      currentInputs.PayMethods = serializePaymentChannels(channelDrafts);

      setInputs(currentInputs);
      setOriginInputs({ ...currentInputs });
      setPaymentChannels(channelDrafts);
      paymentChannelsRef.current = channelDrafts;
      formApiRef.current.setValues(currentInputs);
    }
  }, [props.options]);

  const handleFormChange = (values) => {
    setInputs((prev) => ({ ...prev, ...values }));
  };

  const updatePaymentChannels = (updater) => {
    setPaymentChannels((prev) => {
      const next = typeof updater === 'function' ? updater(prev) : updater;
      paymentChannelsRef.current = next;
      const serialized = serializePaymentChannels(next);
      setInputs((current) => ({ ...current, PayMethods: serialized }));
      formApiRef.current?.setValue('PayMethods', serialized);
      return next;
    });
  };

  const updateChannelField = (type, field, value) => {
    updatePaymentChannels((prev) =>
      prev.map((channel) =>
        channel.type === type ? { ...channel, [field]: value } : channel,
      ),
    );
  };

  const paymentChannelColumns = useMemo(
    () => [
      {
        title: t('通道类型'),
        dataIndex: 'type',
        key: 'type',
        render: (value) => <Text code>{value}</Text>,
      },
      {
        title: t('前台名称'),
        dataIndex: 'name',
        key: 'name',
        render: (_, record) => (
          <Input
            value={record.name}
            placeholder={t('请输入显示名称')}
            onChange={(value) => updateChannelField(record.type, 'name', value)}
          />
        ),
      },
      {
        title: t('启用'),
        dataIndex: 'enabled',
        key: 'enabled',
        render: (_, record) => (
          <Switch
            checked={!!record.enabled}
            onChange={(checked) =>
              updateChannelField(record.type, 'enabled', !!checked)
            }
          />
        ),
      },
      {
        title: t('作用范围'),
        dataIndex: 'scope',
        key: 'scope',
        render: (_, record) => (
          <Select
            value={record.scope}
            optionList={PAYMENT_SCOPE_OPTIONS.map((option) => ({
              value: option.value,
              label: t(option.label),
            }))}
            onChange={(value) => updateChannelField(record.type, 'scope', value)}
          />
        ),
      },
      {
        title: t('最低充值'),
        dataIndex: 'min_topup',
        key: 'min_topup',
        render: (_, record) => (
          <InputNumber
            value={Number(record.min_topup) || 0}
            min={0}
            precision={0}
            onNumberChange={(value) =>
              updateChannelField(record.type, 'min_topup', Number(value) || 0)
            }
          />
        ),
      },
      {
        title: t('主题色'),
        dataIndex: 'color',
        key: 'color',
        render: (_, record) => (
          <div className='flex items-center gap-2'>
            <Input
              value={record.color}
              placeholder='rgba(var(--semi-blue-5), 1)'
              onChange={(value) =>
                updateChannelField(record.type, 'color', value)
              }
            />
            <Tag color='white' style={{ borderColor: record.color || '#d9d9d9' }}>
              <span
                style={{
                  display: 'inline-block',
                  width: 12,
                  height: 12,
                  borderRadius: '50%',
                  background: record.color || '#d9d9d9',
                }}
              />
            </Tag>
          </div>
        ),
      },
      {
        title: t('说明'),
        key: 'scene_label',
        render: (_, record) => <Text type='tertiary'>{t(getPaymentScopeLabel(record.scope))}</Text>,
      },
    ],
    [t],
  );

  const submitPayAddress = async () => {
    const latestPayMethods = serializePaymentChannels(
      paymentChannelsRef.current.length > 0
        ? paymentChannelsRef.current
        : paymentChannels,
    );
    const currentInputs = {
      ...inputs,
      PayMethods: latestPayMethods,
    };

    if (props.options.ServerAddress === '') {
      showError(t('请先填写服务器地址'));
      return;
    }

    if (originInputs['TopupGroupRatio'] !== currentInputs.TopupGroupRatio) {
      if (!verifyJSON(currentInputs.TopupGroupRatio)) {
        showError(t('充值分组倍率不是合法的 JSON 字符串'));
        return;
      }
    }

    if (originInputs['PayMethods'] !== currentInputs.PayMethods) {
      if (!verifyJSON(currentInputs.PayMethods)) {
        showError(t('充值方式设置不是合法的 JSON 字符串'));
        return;
      }
    }

    if (
      originInputs['AmountOptions'] !== currentInputs.AmountOptions &&
      currentInputs.AmountOptions.trim() !== ''
    ) {
      if (!verifyJSON(currentInputs.AmountOptions)) {
        showError(t('自定义充值数量选项不是合法的 JSON 数组'));
        return;
      }
    }

    if (
      originInputs['AmountDiscount'] !== currentInputs.AmountDiscount &&
      currentInputs.AmountDiscount.trim() !== ''
    ) {
      if (!verifyJSON(currentInputs.AmountDiscount)) {
        showError(t('充值金额折扣配置不是合法的 JSON 对象'));
        return;
      }
    }

    setLoading(true);
    try {
      const normalizeOptionalUrl = (value) => {
        const trimmed = (value || '').trim();
        if (!trimmed) {
          return '';
        }
        return removeTrailingSlash(trimmed);
      };

      const options = [
        { key: 'PayAddress', value: normalizeOptionalUrl(currentInputs.PayAddress) },
        { key: 'EpayId', value: (currentInputs.EpayId || '').trim() },
        ...((currentInputs.EpayKey || '').trim() !== ''
          ? [{ key: 'EpayKey', value: currentInputs.EpayKey.trim() }]
          : []),
        { key: 'Price', value: currentInputs.Price.toString() },
        { key: 'MinTopUp', value: currentInputs.MinTopUp.toString() },
        {
          key: 'CustomCallbackAddress',
          value: (currentInputs.CustomCallbackAddress || '').trim(),
        },
      ];

      if (originInputs['TopupGroupRatio'] !== currentInputs.TopupGroupRatio) {
        options.push({ key: 'TopupGroupRatio', value: currentInputs.TopupGroupRatio });
      }
      options.push({ key: 'PayMethods', value: currentInputs.PayMethods });
      if (originInputs['AmountOptions'] !== currentInputs.AmountOptions) {
        options.push({
          key: 'payment_setting.amount_options',
          value: currentInputs.AmountOptions,
        });
      }
      if (originInputs['AmountDiscount'] !== currentInputs.AmountDiscount) {
        options.push({
          key: 'payment_setting.amount_discount',
          value: currentInputs.AmountDiscount,
        });
      }
      options.push(
        {
          key: 'BepusdtApiUrl',
          value: normalizeOptionalUrl(currentInputs.BepusdtApiUrl),
        },
        ...((currentInputs.BepusdtApiToken || '').trim() !== ''
          ? [{ key: 'BepusdtApiToken', value: currentInputs.BepusdtApiToken.trim() }]
          : []),
        { key: 'BepusdtTradeType', value: (currentInputs.BepusdtTradeType || '').trim() },
        { key: 'BepusdtFiat', value: (currentInputs.BepusdtFiat || '').trim() },
        { key: 'BepusdtTimeout', value: currentInputs.BepusdtTimeout.toString() },
        {
          key: 'BepusdtMinPaymentAmount',
          value: currentInputs.BepusdtMinPaymentAmount.toString(),
        },
        {
          key: 'FutoonApiUrl',
          value: normalizeOptionalUrl(currentInputs.FutoonApiUrl),
        },
        { key: 'FutoonPid', value: (currentInputs.FutoonPid || '').trim() },
        ...((currentInputs.FutoonKey || '').trim() !== ''
          ? [{ key: 'FutoonKey', value: currentInputs.FutoonKey.trim() }]
          : []),
        { key: 'FutoonNotifyURL', value: (currentInputs.FutoonNotifyURL || '').trim() },
        { key: 'FutoonReturnURL', value: (currentInputs.FutoonReturnURL || '').trim() },
        {
          key: 'FutoonDefaultDevice',
          value: (currentInputs.FutoonDefaultDevice || '').trim(),
        },
      );

      const errors = [];
      for (const opt of options) {
        const res = await API.put('/api/option/', {
          key: opt.key,
          value: opt.value,
        });
        if (!res.data.success) {
          errors.push(res.data.message);
          break;
        }
      }

      if (errors.length > 0) {
        errors.forEach((message) => {
          showError(message);
        });
        props.refresh && (await props.refresh());
      } else {
        const nextInputs = { ...currentInputs };
        setOriginInputs(nextInputs);
        setInputs(nextInputs);
        formApiRef.current?.setValues(nextInputs);
        showSuccess(t('更新成功'));
        props.refresh && (await props.refresh());
      }
    } catch (error) {
      showError(t('更新失败'));
    }
    setLoading(false);
  };

  return (
    <Spin spinning={loading}>
      <Form
        initValues={inputs}
        onValueChange={handleFormChange}
        getFormApi={(api) => (formApiRef.current = api)}
      >
        <Form.Section text={t('支付通道设置')}>
          <Text>{t('统一管理前台支付通道的启用状态、显示名称、颜色和适用范围。')}</Text>
          <Table
            style={{ marginTop: 16 }}
            columns={paymentChannelColumns}
            dataSource={paymentChannels}
            pagination={false}
            rowKey='type'
          />
          <Form.TextArea field='PayMethods' noLabel autosize style={{ display: 'none' }} />

          <Divider margin='24px' />

          <Form.Section text={t('易支付设置')}>
            <Text>
              {t('（当前仅支持易支付接口，默认使用上方服务器地址作为回调地址！）')}
            </Text>
            <Row gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}>
              <Col xs={24} sm={24} md={8} lg={8} xl={8}>
                <Form.Input
                  field='PayAddress'
                  label={t('支付地址')}
                  placeholder={t('例如：https://yourdomain.com')}
                />
              </Col>
              <Col xs={24} sm={24} md={8} lg={8} xl={8}>
                <Form.Input
                  field='EpayId'
                  label={t('易支付商户ID')}
                  placeholder={t('例如：0001')}
                />
              </Col>
              <Col xs={24} sm={24} md={8} lg={8} xl={8}>
                <Form.Input
                  field='EpayKey'
                  label={t('易支付商户密钥')}
                  placeholder={t('敏感信息不会发送到前端显示')}
                  type='password'
                />
              </Col>
            </Row>
            <Row
              gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}
              style={{ marginTop: 16 }}
            >
              <Col xs={24} sm={24} md={8} lg={8} xl={8}>
                <Form.Input
                  field='CustomCallbackAddress'
                  label={t('回调地址')}
                  placeholder={t('例如：https://yourdomain.com')}
                />
              </Col>
              <Col xs={24} sm={24} md={8} lg={8} xl={8}>
                <Form.InputNumber
                  field='Price'
                  precision={2}
                  label={t('充值价格（x元/美金）')}
                  placeholder={t('例如：7，就是7元/美金')}
                />
              </Col>
              <Col xs={24} sm={24} md={8} lg={8} xl={8}>
                <Form.InputNumber
                  field='MinTopUp'
                  label={t('最低充值美元数量')}
                  placeholder={t('例如：2，就是最低充值2$')}
                />
              </Col>
            </Row>
          </Form.Section>

          <Divider margin='24px' />

          <Form.Section text={t('富通支付设置')}>
            <Text type='tertiary'>
              {t('富通支付配置方式参考易支付：填写支付地址、商户ID、商户密钥与回调地址即可。前台通道对应富通支付宝、富通微信。')}
            </Text>
            <Row gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }} style={{ marginTop: 16 }}>
              <Col xs={24} sm={24} md={12} lg={12} xl={12}>
                <Form.Input
                  field='FutoonApiUrl'
                  label={t('支付地址')}
                  placeholder={t('例如：https://yourdomain.com')}
                />
              </Col>
              <Col xs={24} sm={24} md={12} lg={12} xl={12}>
                <Form.Input
                  field='FutoonPid'
                  label={t('富通商户ID')}
                  placeholder={t('例如：0001')}
                />
              </Col>
            </Row>
            <Row gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }} style={{ marginTop: 16 }}>
              <Col xs={24} sm={24} md={8} lg={8} xl={8}>
                <Form.Input
                  field='FutoonKey'
                  label={t('富通商户密钥')}
                  placeholder={t('敏感信息不会发送到前端显示')}
                  type='password'
                />
              </Col>
              <Col xs={24} sm={24} md={8} lg={8} xl={8}>
                <Form.Input
                  field='FutoonNotifyURL'
                  label={t('回调地址')}
                  placeholder={t('例如：https://yourdomain.com 或完整通知地址')}
                />
              </Col>
              <Col xs={24} sm={24} md={8} lg={8} xl={8}>
                <Form.Input
                  field='FutoonReturnURL'
                  label={t('返回地址')}
                  placeholder={t('可留空，默认返回控制台充值页')}
                />
              </Col>
            </Row>
            <Row gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }} style={{ marginTop: 16 }}>
              <Col xs={24} sm={24} md={8} lg={8} xl={8}>
                <Form.Select
                  field='FutoonDefaultDevice'
                  label={t('默认设备类型')}
                  optionList={[
                    { value: 'pc', label: t('PC') },
                    { value: 'mobile', label: t('移动端') },
                  ]}
                />
              </Col>
            </Row>
          </Form.Section>

          <Divider margin='24px' />

          <Form.Section text={t('充值规则设置')}>
            <Form.TextArea
              field='TopupGroupRatio'
              label={t('充值分组倍率')}
              placeholder={t('为一个 JSON 文本，键为组名称，值为倍率')}
              autosize
            />
            <Row
              gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}
              style={{ marginTop: 16 }}
            >
              <Col span={24}>
                <Form.TextArea
                  field='AmountOptions'
                  label={t('自定义充值数量选项')}
                  placeholder={t('为一个 JSON 数组，例如：[10, 20, 50, 100, 200, 500]')}
                  autosize
                  extraText={t('设置用户可选择的充值数量选项，例如：[10, 20, 50, 100, 200, 500]')}
                />
              </Col>
            </Row>
            <Row
              gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}
              style={{ marginTop: 16 }}
            >
              <Col span={24}>
                <Form.TextArea
                  field='AmountDiscount'
                  label={t('充值金额折扣配置')}
                  placeholder={t('为一个 JSON 对象，例如：{"100": 0.95, "200": 0.9, "500": 0.85}')}
                  autosize
                  extraText={t('设置不同充值金额对应的折扣，键为充值金额，值为折扣率，例如：{"100": 0.95, "200": 0.9, "500": 0.85}')}
                />
              </Col>
            </Row>
          </Form.Section>

          <Divider margin='24px' />

          <Form.Section text={t('Bepusdt 支付设置（USDT加密货币支付）')}>
            <Text type='tertiary'>
              {t('Bepusdt 是一个 USDT 加密货币支付网关，支持 TRC20、ERC20 等多种链。')}
            </Text>
            <Row gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }} style={{ marginTop: 16 }}>
              <Col xs={24} sm={24} md={12} lg={12} xl={12}>
                <Form.Input
                  field='BepusdtApiUrl'
                  label={t('Bepusdt API 地址')}
                  placeholder={t('例如：https://pay.example.com')}
                />
              </Col>
              <Col xs={24} sm={24} md={12} lg={12} xl={12}>
                <Form.Input
                  field='BepusdtApiToken'
                  label={t('Bepusdt API Token')}
                  placeholder={t('对接令牌，敏感信息不会发送到前端显示')}
                  type='password'
                />
              </Col>
            </Row>
            <Row gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }} style={{ marginTop: 16 }}>
              <Col xs={24} sm={24} md={8} lg={8} xl={8}>
                <Form.Input
                  field='BepusdtTradeType'
                  label={t('默认交易类型')}
                  placeholder={t('例如：usdt.trc20')}
                  extraText={t('常用：usdt.trc20, usdt.erc20, usdt.bep20, tron.trx')}
                />
              </Col>
              <Col xs={24} sm={24} md={8} lg={8} xl={8}>
                <Form.Input
                  field='BepusdtFiat'
                  label={t('法币类型')}
                  placeholder={t('例如：CNY')}
                  extraText={t('可选：CNY, USD, EUR, GBP, JPY')}
                />
              </Col>
              <Col xs={24} sm={24} md={8} lg={8} xl={8}>
                <Form.InputNumber
                  field='BepusdtTimeout'
                  label={t('订单超时时间（秒）')}
                  placeholder={t('例如：600')}
                  min={120}
                  extraText={t('最低 120 秒，默认 600 秒')}
                />
              </Col>
            </Row>
            <Row gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }} style={{ marginTop: 16 }}>
              <Col xs={24} sm={24} md={8} lg={8} xl={8}>
                <Form.InputNumber
                  field='BepusdtMinPaymentAmount'
                  label={t('最小支付金额（元）')}
                  placeholder={t('例如：10')}
                  min={0.01}
                  step={0.01}
                  precision={2}
                  extraText={t('当 USDT 实付金额低于该值时，将禁止创建支付订单')}
                />
              </Col>
            </Row>
          </Form.Section>

          <Button type='primary' onClick={submitPayAddress} style={{ marginTop: 16 }}>
            {t('更新支付设置')}
          </Button>
        </Form.Section>
      </Form>
    </Spin>
  );
}
