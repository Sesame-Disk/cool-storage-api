import React from 'react';
import ReactDOM from 'react-dom';
import { act, Simulate } from 'react-dom/test-utils';
import Search from '../search';

describe('sys-admin Search', () => {
    let container = null;

    beforeEach(() => {
        container = document.createElement('div');
        document.body.appendChild(container);
    });

    afterEach(() => {
        ReactDOM.unmountComponentAtNode(container);
        container.remove();
        container = null;
    });

    it('hydrates from initialValue and syncs when the prop changes', () => {
        const submit = jest.fn();

        act(() => {
            ReactDOM.render(
                <Search placeholder="Search links" submit={submit} initialValue="alpha" />,
                container
            );
        });

        let input = container.querySelector('input');
        expect(input.value).toBe('alpha');

        act(() => {
            ReactDOM.render(
                <Search placeholder="Search links" submit={submit} initialValue="beta" />,
                container
            );
        });

        input = container.querySelector('input');
        expect(input.value).toBe('beta');
    });

    it('submits the trimmed value on Enter', () => {
        const submit = jest.fn();

        act(() => {
            ReactDOM.render(<Search placeholder="Search links" submit={submit} />, container);
        });

        const input = container.querySelector('input');

        act(() => {
            Simulate.change(input, { target: { value: '  quarterly report  ' } });
        });
        act(() => {
            Simulate.keyDown(input, { key: 'Enter' });
        });

        expect(submit).toHaveBeenCalledWith('quarterly report');
    });
});