import React from 'react';
import PropTypes from 'prop-types';

const propTypes = {
    placeholder: PropTypes.string.isRequired,
    submit: PropTypes.func.isRequired,
    initialValue: PropTypes.string,
};

class SearchInput extends React.Component {

    constructor(props) {
        super(props);
        this.state = {
            value: props.initialValue || ''
        };
    }

    componentDidUpdate(prevProps) {
        if (prevProps.initialValue !== this.props.initialValue && this.props.initialValue !== this.state.value) {
            this.setState({ value: this.props.initialValue || '' });
        }
    }

    handleInputChange = (e) => {
        this.setState({
            value: e.target.value
        });
    };

    handleKeyDown = (e) => {
        if (e.key === 'Enter') {
            e.preventDefault();
            this.handleSubmit();
        }
    };

    handleSubmit = () => {
        const value = this.state.value.trim();
        if (!value) {
            return false;
        }
        this.props.submit(value);
    };

    render() {
        return (
            <div className="input-icon">
                <i className="d-flex input-icon-addon fas fa-search"></i>
                <input
                    type="text"
                    className="form-control search-input h-6 mr-1"
                    style={{ width: '17rem' }}
                    placeholder={this.props.placeholder}
                    value={this.state.value}
                    onChange={this.handleInputChange}
                    onKeyDown={this.handleKeyDown}
                    autoComplete="off"
                />
            </div>
        );
    }
}

SearchInput.propTypes = propTypes;

export default SearchInput;