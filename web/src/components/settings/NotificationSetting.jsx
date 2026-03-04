import React, { useState, useEffect } from 'react';
import {
    Form,
    Button,
    Toast,
    Typography,
    Space,
    Banner,
} from '@douyinfe/semi-ui';

const { Text, Title } = Typography;

async function apiFetch(url, options = {}) {
    const res = await fetch(url, {
        ...options,
        headers: {
            'Content-Type': 'application/json',
            Authorization: `Bearer ${localStorage.getItem('token')}`,
            ...options.headers,
        },
    });
    return res.json();
}

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
        const data = await apiFetch('/api/notification/config');
        if (data.success && data.data) {
            setConfig(data.data);
        }
    };

    const saveConfig = async (values) => {
        setLoading(true);
        try {
            const data = await apiFetch('/api/notification/config', {
                method: 'PUT',
                body: JSON.stringify({ ...config, ...values }),
            });
            if (data.success) {
                Toast.success(data.message);
                loadConfig();
            } else {
                Toast.error(data.message);
            }
        } finally {
            setLoading(false);
        }
    };

    const testNotification = async () => {
        setTestLoading(true);
        try {
            const data = await apiFetch('/api/notification/test', {
                method: 'POST',
                body: JSON.stringify({ token: config.token }),
            });
            if (data.success) {
                Toast.success(data.message);
            } else {
                Toast.error(data.message);
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
