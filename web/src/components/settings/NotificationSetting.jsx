import React, { useState, useEffect } from 'react';
import {
    Form,
    Button,
    Toast,
    Typography,
    Space,
    Banner,
} from '@douyinfe/semi-ui';
import { API } from '../../helpers/api';

const { Text, Title } = Typography;

const NotificationSetting = () => {
    const [config, setConfig] = useState({
        type: 'pushplus',
        token: '',
        enabled: false,
    });
    const [loading, setLoading] = useState(false);
    const [testLoading, setTestLoading] = useState(false);

    useEffect(() => {
        loadConfig();
    }, []);

    const loadConfig = async () => {
        try {
            const res = await API.get('/api/notification/config');
            if (res.data.success && res.data.data) {
                setConfig(res.data.data);
            }
        } catch (e) {
            // ignore
        }
    };

    const saveConfig = async (values) => {
        setLoading(true);
        try {
            const res = await API.put('/api/notification/config', { ...config, ...values });
            if (res.data.success) {
                Toast.success(res.data.message);
                loadConfig();
            } else {
                Toast.error(res.data.message);
            }
        } finally {
            setLoading(false);
        }
    };

    const testNotification = async () => {
        setTestLoading(true);
        try {
            const res = await API.post('/api/notification/test', { token: config.token });
            if (res.data.success) {
                Toast.success(res.data.message);
            } else {
                Toast.error(res.data.message);
            }
        } finally {
            setTestLoading(false);
        }
    };

    return (
        <div style={{ maxWidth: 600, padding: 16 }}>
            <Title heading={5} style={{ marginBottom: 16 }}>通知设置</Title>
            <Banner
                type='info'
                description='配置 PushPlus 通知后，分组倍率变动和分组变动将自动推送通知。'
                style={{ marginBottom: 16 }}
            />

            <Form
                initValues={config}
                onSubmit={saveConfig}
                labelPosition='left'
                labelWidth={120}
            >
                <Form.Select
                    field='type'
                    label='通知方式'
                    optionList={[{ label: 'PushPlus', value: 'pushplus' }]}
                    disabled
                />
                <Form.Input
                    field='token'
                    label='PushPlus Token'
                    placeholder='请输入 PushPlus Token'
                    onChange={(v) => setConfig({ ...config, token: v })}
                />
                <Form.Switch field='enabled' label='启用通知' />

                <div style={{ marginTop: 16 }}>
                    <Space>
                        <Button htmlType='submit' theme='solid' loading={loading}>
                            保存配置
                        </Button>
                        <Button loading={testLoading} onClick={testNotification}>
                            发送测试通知
                        </Button>
                    </Space>
                </div>
            </Form>

            <div style={{ marginTop: 24 }}>
                <Text type='tertiary'>
                    PushPlus 注册地址:{' '}
                    <a href='https://www.pushplus.plus' target='_blank' rel='noopener noreferrer'>
                        https://www.pushplus.plus
                    </a>
                </Text>
            </div>
        </div>
    );
};

export default NotificationSetting;
