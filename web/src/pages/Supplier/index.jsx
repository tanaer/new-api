import React, { useState, useEffect, useCallback } from 'react';
import {
    Table,
    Button,
    Modal,
    Form,
    Space,
    Tag,
    Popconfirm,
    Toast,
    InputNumber,
    Typography,
    SideSheet,
    Descriptions,
    Select,
    Input,
    Banner,
    Spin,
} from '@douyinfe/semi-ui';
import {
    IconPlus,
    IconRefresh,
    IconDelete,
    IconEdit,
    IconSetting,
    IconSearch,
} from '@douyinfe/semi-icons';

const { Text } = Typography;

const API_BASE = '/api/supplier';

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

const SupplierPage = () => {
    const [suppliers, setSuppliers] = useState([]);
    const [loading, setLoading] = useState(false);
    const [showEdit, setShowEdit] = useState(false);
    const [editingSupplier, setEditingSupplier] = useState(null);
    const [showGroups, setShowGroups] = useState(false);
    const [currentSupplier, setCurrentSupplier] = useState(null);
    const [groups, setGroups] = useState([]);
    const [groupsLoading, setGroupsLoading] = useState(false);
    const [fetchingGroups, setFetchingGroups] = useState(false);
    const [bulkMarkupVisible, setBulkMarkupVisible] = useState(false);
    const [bulkMarkup, setBulkMarkup] = useState(1.1);
    const [existingGroups, setExistingGroups] = useState([]);

    const loadSuppliers = useCallback(async () => {
        setLoading(true);
        try {
            const data = await apiFetch(`${API_BASE}/`);
            if (data.success) setSuppliers(data.data || []);
        } finally {
            setLoading(false);
        }
    }, []);

    // 加载本地分组列表
    const loadExistingGroups = useCallback(async () => {
        try {
            const data = await apiFetch('/api/group/');
            if (data.success) {
                const groupNames = data.data || [];
                setExistingGroups(
                    groupNames.map((g) => ({ label: g, value: g })),
                );
            }
        } catch (e) {
            // ignore
        }
    }, []);

    useEffect(() => {
        loadSuppliers();
        loadExistingGroups();
    }, [loadSuppliers, loadExistingGroups]);

    // 打开编辑
    const openEdit = (supplier = null) => {
        setEditingSupplier(
            supplier || { name: '', base_url: '', username: '', password: '', cookie: '', markup: 1.1, status: 1 },
        );
        setShowEdit(true);
    };

    // 保存供应商
    const saveSupplier = async (values) => {
        const isNew = !editingSupplier?.id;
        const url = isNew ? `${API_BASE}/` : `${API_BASE}/`;
        const method = isNew ? 'POST' : 'PUT';
        const body = { ...editingSupplier, ...values };
        const data = await apiFetch(url, { method, body: JSON.stringify(body) });
        if (data.success) {
            Toast.success(data.message);
            setShowEdit(false);
            loadSuppliers();
        } else {
            Toast.error(data.message);
        }
    };

    // 删除供应商
    const deleteSupplier = async (id) => {
        const data = await apiFetch(`${API_BASE}/${id}`, { method: 'DELETE' });
        if (data.success) {
            Toast.success(data.message);
            loadSuppliers();
        } else {
            Toast.error(data.message);
        }
    };

    // 打开分组管理
    const openGroups = async (supplier) => {
        setCurrentSupplier(supplier);
        setShowGroups(true);
        setGroupsLoading(true);
        try {
            const data = await apiFetch(`${API_BASE}/${supplier.id}`);
            if (data.success) {
                setGroups(data.data.groups || []);
            }
        } finally {
            setGroupsLoading(false);
        }
    };

    // 一键采集分组
    const fetchGroups = async () => {
        if (!currentSupplier) return;
        setFetchingGroups(true);
        try {
            const data = await apiFetch(`${API_BASE}/${currentSupplier.id}/fetch_groups`, { method: 'POST' });
            if (data.success) {
                Toast.success(data.message);
                setGroups(data.data || []);
            } else {
                Toast.error(data.message);
            }
        } finally {
            setFetchingGroups(false);
        }
    };

    // 更新分组配置
    const updateGroup = async (group) => {
        const data = await apiFetch(`${API_BASE}/group`, {
            method: 'PUT',
            body: JSON.stringify(group),
        });
        if (data.success) {
            Toast.success('更新成功');
        } else {
            Toast.error(data.message);
        }
    };

    // 修改单个供应商倍率
    const updateMarkup = async (supplierId, markup) => {
        const data = await apiFetch(`${API_BASE}/${supplierId}/markup`, {
            method: 'PUT',
            body: JSON.stringify({ markup }),
        });
        if (data.success) {
            Toast.success(data.message);
            loadSuppliers();
        } else {
            Toast.error(data.message);
        }
    };

    // 一键设置所有倍率
    const bulkUpdateMarkup = async () => {
        const data = await apiFetch(`${API_BASE}/bulk_markup`, {
            method: 'PUT',
            body: JSON.stringify({ markup: bulkMarkup }),
        });
        if (data.success) {
            Toast.success(data.message);
            setBulkMarkupVisible(false);
            loadSuppliers();
        } else {
            Toast.error(data.message);
        }
    };

    // 查询余额
    const checkBalance = async (supplierId) => {
        const data = await apiFetch(`${API_BASE}/${supplierId}/check_balance`, { method: 'POST' });
        if (data.success) {
            Toast.info(`余额信息: ${JSON.stringify(data.data)}`);
        } else {
            Toast.error(data.message);
        }
    };

    const columns = [
        { title: 'ID', dataIndex: 'id', width: 60 },
        { title: '名称', dataIndex: 'name', width: 150 },
        {
            title: 'API 地址',
            dataIndex: 'base_url',
            width: 250,
            render: (text) => (
                <Text ellipsis={{ showTooltip: true }} style={{ maxWidth: 230 }}>
                    {text}
                </Text>
            ),
        },
        {
            title: '倍率',
            dataIndex: 'markup',
            width: 100,
            render: (text, record) => (
                <Tag color={text >= 1 ? 'green' : 'red'} style={{ cursor: 'pointer' }}>
                    ×{text?.toFixed(2)}
                </Tag>
            ),
        },
        {
            title: '状态',
            dataIndex: 'status',
            width: 80,
            render: (status) => (
                <Tag color={status === 1 ? 'green' : 'red'}>
                    {status === 1 ? '启用' : '禁用'}
                </Tag>
            ),
        },
        {
            title: '操作',
            width: 300,
            render: (_, record) => (
                <Space>
                    <Button size='small' icon={<IconEdit />} onClick={() => openEdit(record)}>
                        编辑
                    </Button>
                    <Button size='small' icon={<IconSetting />} onClick={() => openGroups(record)}>
                        分组
                    </Button>
                    <Button size='small' onClick={() => checkBalance(record.id)}>
                        余额
                    </Button>
                    <Popconfirm title='确认删除？' onConfirm={() => deleteSupplier(record.id)}>
                        <Button size='small' type='danger' icon={<IconDelete />}>
                            删除
                        </Button>
                    </Popconfirm>
                </Space>
            ),
        },
    ];

    // 分组列
    const groupColumns = [
        { title: '上游分组', dataIndex: 'upstream_group', width: 120 },
        {
            title: '分组倍率',
            dataIndex: 'group_ratio',
            width: 100,
            render: (v) => <Tag color='blue'>×{v?.toFixed(4)}</Tag>,
        },
        {
            title: 'API密钥',
            dataIndex: 'api_key',
            width: 200,
            render: (text, record, index) => (
                <Input
                    defaultValue={text}
                    placeholder='输入API密钥'
                    size='small'
                    onBlur={(e) => {
                        const newGroups = [...groups];
                        newGroups[index] = { ...newGroups[index], api_key: e.target.value };
                        setGroups(newGroups);
                        updateGroup({ ...newGroups[index] });
                    }}
                />
            ),
        },
        {
            title: '本地分组',
            dataIndex: 'local_group',
            width: 180,
            render: (text, record, index) => (
                <Select
                    defaultValue={text || undefined}
                    placeholder='选择本地分组'
                    size='small'
                    filter
                    allowCreate
                    style={{ width: 160 }}
                    optionList={existingGroups}
                    onChange={(value) => {
                        const newGroups = [...groups];
                        newGroups[index] = { ...newGroups[index], local_group: value };
                        setGroups(newGroups);
                        updateGroup({ ...newGroups[index] });
                    }}
                />
            ),
        },
    ];

    return (
        <div className='mt-[60px] px-2'>
            <div style={{ marginBottom: 16, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <Space>
                    <Button icon={<IconPlus />} theme='solid' onClick={() => openEdit()}>
                        添加供应商
                    </Button>
                    <Button icon={<IconRefresh />} onClick={loadSuppliers}>
                        刷新
                    </Button>
                </Space>
                <Button theme='light' type='warning' onClick={() => setBulkMarkupVisible(true)}>
                    一键设置所有倍率
                </Button>
            </div>

            <Table
                columns={columns}
                dataSource={suppliers}
                rowKey='id'
                loading={loading}
                pagination={false}
            />

            {/* 编辑供应商 SideSheet */}
            <SideSheet
                title={editingSupplier?.id ? '编辑供应商' : '添加供应商'}
                visible={showEdit}
                onCancel={() => setShowEdit(false)}
                width={480}
            >
                {editingSupplier && (
                    <Form
                        initValues={editingSupplier}
                        onSubmit={saveSupplier}
                        labelPosition='left'
                        labelWidth={100}
                    >
                        <Form.Input field='name' label='名称' rules={[{ required: true, message: '请输入名称' }]} />
                        <Form.Input field='base_url' label='API地址' rules={[{ required: true, message: '请输入API地址' }]} />
                        <Form.Input field='username' label='账号' />
                        <Form.Input field='password' label='密码' mode='password' />
                        <Form.TextArea field='cookie' label='Cookie' placeholder='有Cookie优先使用Cookie' autosize rows={2} />
                        <Form.InputNumber field='markup' label='加价倍率' min={0} step={0.01} />
                        <Form.Select field='status' label='状态' optionList={[
                            { label: '启用', value: 1 },
                            { label: '禁用', value: 2 },
                        ]} />
                        <div style={{ marginTop: 16 }}>
                            <Space>
                                <Button htmlType='submit' theme='solid'>保存</Button>
                                <Button onClick={() => setShowEdit(false)}>取消</Button>
                            </Space>
                        </div>
                    </Form>
                )}
            </SideSheet>

            {/* 分组管理 SideSheet */}
            <SideSheet
                title={`分组管理 - ${currentSupplier?.name || ''}`}
                visible={showGroups}
                onCancel={() => setShowGroups(false)}
                width={750}
            >
                <div style={{ marginBottom: 16 }}>
                    <Space>
                        <Button icon={<IconSearch />} loading={fetchingGroups} onClick={fetchGroups} theme='solid'>
                            一键采集分组
                        </Button>
                        {currentSupplier && (
                            <Descriptions
                                row
                                size='small'
                                data={[
                                    { key: '当前倍率', value: `×${currentSupplier.markup?.toFixed(2)}` },
                                ]}
                            />
                        )}
                    </Space>
                </div>

                {groupsLoading ? (
                    <Spin size='large' />
                ) : groups.length > 0 ? (
                    <Table
                        columns={groupColumns}
                        dataSource={groups}
                        rowKey='id'
                        pagination={false}
                        size='small'
                    />
                ) : (
                    <Banner type='info' description='暂无分组数据，请先采集分组' />
                )}

                {currentSupplier && (
                    <div style={{ marginTop: 24, padding: 16, background: 'var(--semi-color-fill-0)', borderRadius: 8 }}>
                        <Text strong>调整倍率并重算通道状态</Text>
                        <div style={{ marginTop: 8, display: 'flex', gap: 8, alignItems: 'center' }}>
                            <InputNumber
                                defaultValue={currentSupplier.markup}
                                min={0}
                                step={0.01}
                                style={{ width: 120 }}
                                onChange={(v) => setCurrentSupplier({ ...currentSupplier, markup: v })}
                            />
                            <Button
                                theme='solid'
                                type='warning'
                                onClick={() => updateMarkup(currentSupplier.id, currentSupplier.markup)}
                            >
                                应用并调整通道
                            </Button>
                        </div>
                    </div>
                )}
            </SideSheet>

            {/* 一键设置倍率 Modal */}
            <Modal
                title='一键设置所有供应商倍率'
                visible={bulkMarkupVisible}
                onCancel={() => setBulkMarkupVisible(false)}
                onOk={bulkUpdateMarkup}
                okText='确认设置'
            >
                <div style={{ padding: '16px 0' }}>
                    <Text>设置后，所有供应商的倍率将统一调整为指定值，并自动重算通道状态。</Text>
                    <div style={{ marginTop: 16 }}>
                        <InputNumber
                            value={bulkMarkup}
                            min={0}
                            step={0.01}
                            style={{ width: 200 }}
                            onChange={setBulkMarkup}
                            prefix='×'
                        />
                    </div>
                </div>
            </Modal>
        </div>
    );
};

export default SupplierPage;
